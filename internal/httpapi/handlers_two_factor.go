package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/auth"
	"hawkeye/internal/organizations"
	"hawkeye/internal/twofactor"
)

// Two-factor sign-in (internal/twofactor): the second sign-in step, the account settings, and an owner's requirement for their organization.

var (
	errTwoFactorOff = &APIError{Status: http.StatusServiceUnavailable, Code: "TWO_FACTOR_UNAVAILABLE",
		Message: "Two-factor sign-in isn't available on this server."}
	errSignInExpired = &APIError{Status: http.StatusUnauthorized, Code: "SIGN_IN_EXPIRED",
		Message: "That sign-in timed out or had too many wrong codes. Enter your email and password again."}
	errTwoFactorRequired = &APIError{Status: http.StatusForbidden, Code: "TWO_FACTOR_REQUIRED",
		Message: "This organization requires two-factor sign-in. Set it up on your account page to use it again."}
)

func mapTwoFactorError(err error) error {
	switch {
	case errors.Is(err, twofactor.ErrBadCode):
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_CODE", Message: "That code is not correct. Check your authenticator app and try again."}
	case errors.Is(err, twofactor.ErrAlreadyEnabled):
		return &APIError{Status: http.StatusConflict, Code: "TWO_FACTOR_ALREADY_ON", Message: "Two-factor sign-in is already on."}
	case errors.Is(err, twofactor.ErrNotEnabled):
		return &APIError{Status: http.StatusConflict, Code: "TWO_FACTOR_OFF", Message: "Two-factor sign-in is not on."}
	case errors.Is(err, twofactor.ErrNoSetup):
		return &APIError{Status: http.StatusConflict, Code: "NO_TWO_FACTOR_SETUP", Message: "Start setting up two-factor sign-in first."}
	case errors.Is(err, twofactor.ErrRequiredByOrganization):
		return &APIError{Status: http.StatusConflict, Code: "TWO_FACTOR_REQUIRED_BY_ORGANIZATION",
			Message: "An organization you own requires two-factor sign-in, so you can't turn yours off. Stop requiring it on the Team page first."}
	case errors.Is(err, twofactor.ErrOwnerNeedsTwoFactor):
		return &APIError{Status: http.StatusConflict, Code: "OWNER_NEEDS_TWO_FACTOR",
			Message: "Turn on two-factor sign-in for your own account first, so you can't be shut out of your organization."}
	case errors.Is(err, twofactor.ErrNotOwner):
		return mapError(organizations.ErrForbidden)
	}
	return mapError(err)
}

func (h *handlers) needTwoFactor() error {
	if h.twoFactor == nil {
		return errTwoFactorOff
	}
	return nil
}

// login answers a correct password for an account with two-factor sign-in with a challenge instead of a session.
func (h *handlers) secondFactorChallenge(c *echo.Context, err error) (bool, error) {
	var need *auth.SecondFactorRequired
	if !errors.As(err, &need) {
		return false, nil
	}
	return true, c.JSON(http.StatusOK, map[string]any{"two_factor_required": true, "challenge": need.Challenge})
}

func (h *handlers) loginTwoFactor(c *echo.Context) error {
	if err := h.needTwoFactor(); err != nil {
		return err
	}
	var in struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	u, session, left, usedRecovery, err := h.auth.CompleteSignIn(c.Request().Context(), in.Challenge, in.Code)
	switch {
	case errors.Is(err, auth.ErrChallengeInvalid):
		return errSignInExpired
	case errors.Is(err, twofactor.ErrBadCode):
		return &APIError{Status: http.StatusUnauthorized, Code: "INVALID_CODE", Message: "That code is not correct. Check your authenticator app and try again.",
			Details: map[string]any{"tries_left": left}}
	case err != nil:
		return mapError(err)
	}
	h.setSessionCookie(c, session)
	return c.JSON(http.StatusOK, map[string]any{"user": u, "used_recovery_code": usedRecovery})
}

func (h *handlers) twoFactorStatus(c *echo.Context) error {
	if err := h.needTwoFactor(); err != nil {
		return err
	}
	st, err := h.twoFactor.Status(c.Request().Context(), currentUser(c).ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"two_factor": st})
}

// twoFactorSetup starts setting up an authenticator app. It asks for the password again: a session left open on a shared computer must not be
// enough to tie the account to someone else's phone.
func (h *handlers) twoFactorSetup(c *echo.Context) error {
	if err := h.needTwoFactor(); err != nil {
		return err
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	u := currentUser(c)
	ctx := c.Request().Context()
	if err := h.auth.VerifyPassword(ctx, u.ID, in.Password); err != nil {
		return mapError(err)
	}
	setup, err := h.twoFactor.BeginSetup(ctx, u.ID, u.Email)
	if err != nil {
		return mapTwoFactorError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"setup": setup})
}

func (h *handlers) twoFactorEnable(c *echo.Context) error {
	if err := h.needTwoFactor(); err != nil {
		return err
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	u := currentUser(c)
	codes, err := h.twoFactor.Enable(c.Request().Context(), u.ID, u.Email, in.Code)
	if err != nil {
		return mapTwoFactorError(err)
	}
	h.recordAccount(c, audit.TwoFactorEnabled, nil)
	return c.JSON(http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (h *handlers) twoFactorDisable(c *echo.Context) error {
	if err := h.needTwoFactor(); err != nil {
		return err
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	u := currentUser(c)
	ctx := c.Request().Context()
	if err := h.auth.VerifyPassword(ctx, u.ID, in.Password); err != nil {
		return mapError(err)
	}
	if err := h.twoFactor.Disable(ctx, u.ID, u.Email, in.Code); err != nil {
		return mapTwoFactorError(err)
	}
	h.recordAccount(c, audit.TwoFactorDisabled, nil)
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) twoFactorRecoveryCodes(c *echo.Context) error {
	if err := h.needTwoFactor(); err != nil {
		return err
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	u := currentUser(c)
	codes, err := h.twoFactor.RegenerateRecoveryCodes(c.Request().Context(), u.ID, u.Email, in.Code)
	if err != nil {
		return mapTwoFactorError(err)
	}
	h.recordAccount(c, audit.RecoveryCodesRegenerated, nil)
	return c.JSON(http.StatusOK, map[string]any{"recovery_codes": codes})
}

// orgTwoFactor shows the owner whether the organization requires two-factor sign-in and which members use it.
func (h *handlers) orgTwoFactor(c *echo.Context) error {
	if err := h.needTwoFactor(); err != nil {
		return err
	}
	id, err := h.ownedOrg(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	req, err := h.twoFactor.For(ctx, currentUser(c).ID, id)
	if err != nil {
		return err
	}
	members, err := h.twoFactor.Members(ctx, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"required": req.Required, "grace_days": int(twofactor.GracePeriod.Hours() / 24), "members": members})
}

func (h *handlers) setOrgTwoFactor(c *echo.Context) error {
	if err := h.needTwoFactor(); err != nil {
		return err
	}
	id, err := orgID(c)
	if err != nil {
		return err
	}
	var in struct {
		Required bool `json:"required"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := h.twoFactor.SetRequired(c.Request().Context(), currentUser(c).ID, id, in.Required); err != nil {
		return mapTwoFactorError(err)
	}
	h.recordIn(c, id, audit.TwoFactorRequirement, target{}, map[string]any{"required": in.Required})
	return h.orgTwoFactor(c)
}

// blockedByTwoFactor reports whether the session's person is shut out of the organization by its two-factor requirement.
func (h *handlers) blockedByTwoFactor(c *echo.Context, orgID uuid.UUID) error {
	if h.twoFactor == nil {
		return nil
	}
	r, err := h.twoFactor.For(c.Request().Context(), currentUser(c).ID, orgID)
	if err != nil {
		return err
	}
	if r.Blocked {
		return errTwoFactorRequired
	}
	return nil
}
