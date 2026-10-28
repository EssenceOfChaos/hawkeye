package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"hawkeye/internal/invitations"
	"hawkeye/internal/organizations"
	"hawkeye/internal/validation"
)

// ownedOrg returns the organization named in the path when the caller is its owner. Members, and people who do not belong to it at all, get the same
// answers as everywhere else: 403 for a member, 404 for a stranger.
func (h *handlers) ownedOrg(c echo.Context) (uuid.UUID, error) {
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

func (h *handlers) listInvitations(c echo.Context) error {
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
func (h *handlers) createInvitation(c echo.Context) error {
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
	return c.JSON(http.StatusCreated, map[string]any{"invitation": inv})
}

func (h *handlers) resendInvitation(c echo.Context) error {
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
	return c.JSON(http.StatusOK, map[string]any{"invitation": inv})
}

func (h *handlers) revokeInvitation(c echo.Context) error {
	id, err := h.ownedOrg(c)
	if err != nil {
		return err
	}
	invID, err := uuid.Parse(c.Param("invitationID"))
	if err != nil {
		return mapError(invitations.ErrNotFound)
	}
	if err := h.invitations.Revoke(c.Request().Context(), id, invID); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) removeMember(c echo.Context) error {
	id, err := orgID(c)
	if err != nil {
		return err
	}
	memberID, err := uuid.Parse(c.Param("userID"))
	if err != nil {
		return mapError(organizations.ErrMemberNotFound)
	}
	if err := h.orgs.RemoveMember(c.Request().Context(), currentUser(c).ID, id, memberID); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) leaveOrg(c echo.Context) error {
	id, err := orgID(c)
	if err != nil {
		return err
	}
	if err := h.orgs.Leave(c.Request().Context(), currentUser(c).ID, id); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// previewInvitation is called from the link in the invitation email by someone who may not be signed in, or may not have an account yet.
func (h *handlers) previewInvitation(c echo.Context) error {
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

func (h *handlers) acceptInvitation(c echo.Context) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if in.Token == "" {
		return mapError(validation.New("token", "is required"))
	}
	o, err := h.invitations.Accept(c.Request().Context(), currentUser(c), in.Token)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"organization": o})
}
