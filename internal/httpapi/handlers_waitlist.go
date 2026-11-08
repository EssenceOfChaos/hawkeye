package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/auth"
	"hawkeye/internal/waitlist"
)

// Waitlists for coming-soon features (internal/waitlist). Public: visitors sign up from the landing page. A signed-in person with a verified
// address is added at once; anyone else confirms from an email, which has its own per-address budget so the form can't be used to mail-bomb
// someone. The answer never says whether an address was already on a list.

var errWaitlistOff = &APIError{Status: http.StatusServiceUnavailable, Code: "WAITLIST_UNAVAILABLE", Message: "Signing up isn't available right now."}

// sessionUser returns the signed-in person, if the request carries a valid session (best effort, like the contact form).
func (h *handlers) sessionUser(c *echo.Context) (auth.User, bool) {
	cookie, err := c.Cookie(sessionCookie)
	if err != nil || h.auth == nil {
		return auth.User{}, false
	}
	u, err := h.auth.Authenticate(c.Request().Context(), cookie.Value)
	return u, err == nil
}

func (h *handlers) joinWaitlist(c *echo.Context) error {
	if h.waitlist == nil {
		return errWaitlistOff
	}
	var in struct {
		Feature string `json:"feature"`
		Email   string `json:"email"`
		Company string `json:"company"` // honeypot: hidden from people, filled in by form-spamming bots
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if in.Company != "" {
		return c.JSON(http.StatusAccepted, map[string]string{"status": string(waitlist.CheckInbox)})
	}
	if _, ok := waitlist.Features[in.Feature]; !ok {
		return &APIError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "That isn't a feature you can sign up for.",
			Details: map[string]any{"fields": map[string]string{"feature": "is not a feature you can sign up for"}}}
	}
	ctx := c.Request().Context()
	// Signed in with a verified address, and not typing a different one: no confirmation needed.
	if u, ok := h.sessionUser(c); ok && u.EmailVerified() && (in.Email == "" || strings.EqualFold(strings.TrimSpace(in.Email), u.Email)) {
		st, err := h.waitlist.JoinVerified(ctx, in.Feature, u)
		if err != nil {
			return mapError(err)
		}
		return c.JSON(http.StatusOK, map[string]string{"status": string(st)})
	}
	addr, err := auth.NormalizeEmail(in.Email)
	if err != nil {
		return mapError(err)
	}
	// Over the address's hourly email budget: answer as if it went, without sending (no enumeration, no mail-bombing).
	if h.emailBudget(c, "waitlist", addr) {
		if _, err := h.waitlist.Join(ctx, in.Feature, addr); err != nil {
			return mapError(err)
		}
	}
	return c.JSON(http.StatusAccepted, map[string]string{"status": string(waitlist.CheckInbox)})
}

func (h *handlers) confirmWaitlist(c *echo.Context) error {
	if h.waitlist == nil {
		return errWaitlistOff
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	name, err := h.waitlist.Confirm(c.Request().Context(), in.Token)
	if errors.Is(err, waitlist.ErrInvalidToken) {
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_TOKEN", Message: "This link is invalid or has expired. Sign up again on the home page."}
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{"feature": name})
}

func (h *handlers) leaveWaitlist(c *echo.Context) error {
	if h.waitlist == nil {
		return errWaitlistOff
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := h.waitlist.Leave(c.Request().Context(), in.Token); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
