package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/invitations"
	"hawkeye/internal/organizations"
	"hawkeye/internal/validation"
)

// ownedOrg returns the organization named in the path when the caller is its owner. Members, and people who do not belong to it at all, get the same
// answers as everywhere else: 403 for a member, 404 for a stranger.
func (h *handlers) ownedOrg(c *echo.Context) (uuid.UUID, error) {
	id, err := orgID(c)
	if err != nil {
		return uuid.Nil, err
	}
	o, err := h.orgs.Get(c.Request().Context(), currentUser(c).ID, id)
	if err != nil {
		return uuid.Nil, mapError(err)
	}
	if o.Role != organizations.RoleOwner {
		return uuid.Nil, mapError(organizations.ErrForbidden)
	}
	return id, nil
}

func (h *handlers) listInvitations(c *echo.Context) error {
	id, err := h.ownedOrg(c)
	if err != nil {
		return err
	}
	list, err := h.invitations.List(c.Request().Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"invitations": list})
}

// createInvitation emails a third party, so it needs a verified inviter and is capped per target address as well as per IP.
func (h *handlers) createInvitation(c *echo.Context) error {
	id, err := h.ownedOrg(c)
	if err != nil {
		return err
	}
	var in struct {
		Email string `json:"email"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if !h.emailBudget(c, "invite", strings.ToLower(strings.TrimSpace(in.Email))) {
		c.Response().Header().Set("Retry-After", "3600")
		return errRateLimited
	}
	inv, err := h.invitations.Invite(c.Request().Context(), id, currentUser(c), in.Email)
	if err != nil {
		return mapError(err)
	}
	h.recordIn(c, id, audit.MemberInvited, target{"invitation", inv.ID.String(), inv.Email}, nil)
	return c.JSON(http.StatusCreated, map[string]any{"invitation": inv})
}

func (h *handlers) resendInvitation(c *echo.Context) error {
	id, err := h.ownedOrg(c)
	if err != nil {
		return err
	}
	invID, err := uuid.Parse(c.Param("invitationID"))
	if err != nil {
		return mapError(invitations.ErrNotFound)
	}
	if !h.emailBudget(c, "invite-resend", invID.String()) {
		c.Response().Header().Set("Retry-After", "3600")
		return errRateLimited
	}
	inv, err := h.invitations.Resend(c.Request().Context(), id, invID, currentUser(c))
	if err != nil {
		return mapError(err)
	}
	h.recordIn(c, id, audit.InvitationResent, target{"invitation", inv.ID.String(), inv.Email}, nil)
	return c.JSON(http.StatusOK, map[string]any{"invitation": inv})
}

func (h *handlers) revokeInvitation(c *echo.Context) error {
	id, err := h.ownedOrg(c)
	if err != nil {
		return err
	}
	invID, err := uuid.Parse(c.Param("invitationID"))
	if err != nil {
		return mapError(invitations.ErrNotFound)
	}
	addr := h.nameIn(c, id, `SELECT email FROM organization_invitations WHERE id = $1 AND organization_id = $2`, invID)
	if err := h.invitations.Revoke(c.Request().Context(), id, invID); err != nil {
		return mapError(err)
	}
	h.recordIn(c, id, audit.InvitationRevoked, target{"invitation", invID.String(), addr}, nil)
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) removeMember(c *echo.Context) error {
	id, err := orgID(c)
	if err != nil {
		return err
	}
	memberID, err := uuid.Parse(c.Param("userID"))
	if err != nil {
		return mapError(organizations.ErrMemberNotFound)
	}
	addr := h.nameIn(c, id, memberEmailQuery, memberID)
	if err := h.orgs.RemoveMember(c.Request().Context(), currentUser(c).ID, id, memberID); err != nil {
		return mapError(err)
	}
	h.recordIn(c, id, audit.MemberRemoved, target{"member", memberID.String(), addr}, nil)
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) leaveOrg(c *echo.Context) error {
	id, err := orgID(c)
	if err != nil {
		return err
	}
	if err := h.orgs.Leave(c.Request().Context(), currentUser(c).ID, id); err != nil {
		return mapError(err)
	}
	u := currentUser(c)
	h.recordIn(c, id, audit.MemberLeft, target{"member", u.ID.String(), u.Email}, nil)
	return c.NoContent(http.StatusNoContent)
}

// previewInvitation is called from the link in the invitation email by someone who may not be signed in, or may not have an account yet.
func (h *handlers) previewInvitation(c *echo.Context) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	p, err := h.invitations.Preview(c.Request().Context(), in.Token)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"invitation": p})
}

func (h *handlers) acceptInvitation(c *echo.Context) error {
	var in struct {
		Token  string `json:"token"`
		Alerts bool   `json:"alerts"` // also receive the organization's alerts at the account's own address
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if in.Token == "" {
		return mapError(validation.New("token", "is required"))
	}
	res, err := h.invitations.Accept(c.Request().Context(), currentUser(c), in.Token, in.Alerts)
	if err != nil {
		return mapError(err)
	}
	u := currentUser(c)
	h.recordIn(c, res.Organization.ID, audit.MemberJoined, target{"member", u.ID.String(), u.Email}, nil)
	return c.JSON(http.StatusOK, res)
}

// transferOwnership hands the organization to one of its members. The old owner stays as a member. Billing email follows the new owner; if the payment
// provider cannot be reached the transfer still stands, and the address can be changed in the billing portal.
func (h *handlers) transferOwnership(c *echo.Context) error {
	id, err := h.ownedOrg(c)
	if err != nil {
		return err
	}
	var in struct {
		UserID      uuid.UUID `json:"user_id"`
		ConfirmName string    `json:"confirm_name"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	if h.twoFactor != nil {
		// The new owner must not be shut out of the organization they now own.
		if r, err := h.twoFactor.For(ctx, in.UserID, id); err != nil {
			return err
		} else if r.Required && r.GraceEndsAt != nil {
			return &APIError{Status: http.StatusConflict, Code: "NEW_OWNER_NEEDS_TWO_FACTOR",
				Message: "This organization requires two-factor sign-in, and that member hasn't set it up yet. Ask them to, then transfer ownership."}
		}
	}
	newOwner, err := h.invitations.Transfer(ctx, id, currentUser(c), in.UserID, in.ConfirmName)
	if err != nil {
		return mapError(err)
	}
	h.recordIn(c, id, audit.OwnershipTransferred, target{"member", in.UserID.String(), newOwner}, map[string]any{"from": currentUser(c).Email})
	if h.billing != nil {
		if err := h.billing.UpdateCustomerEmail(ctx, id, newOwner); err != nil {
			h.log.ErrorContext(ctx, "update billing email after ownership transfer failed", "organization_id", id, "error", err)
		}
	}
	return c.NoContent(http.StatusNoContent)
}
