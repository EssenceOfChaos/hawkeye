package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// unsubscribeLifecycle stops one list of optional email (?list=onboarding, the default, or weekly_summary) for the person the link's token belongs to. The token comes from the query string, not a body, because
// a mail client's one-click POST (RFC 8058) goes to exactly the URL from the List-Unsubscribe header and sends only "List-Unsubscribe=One-Click".
// It succeeds again for someone already unsubscribed. An unknown token is an error, so the page can say the link is wrong.
func (h *handlers) unsubscribeLifecycle(c *echo.Context) error {
	if err := h.lifecycle.Unsubscribe(c.Request().Context(), c.QueryParam("token"), c.QueryParam("list")); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// emailPreferences returns the signed-in person's optional emails (the weekly summary and the getting-started emails).
func (h *handlers) emailPreferences(c *echo.Context) error {
	p, err := h.lifecycle.Preferences(c.Request().Context(), currentUser(c).ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"email_preferences": p})
}

func (h *handlers) putEmailPreferences(c *echo.Context) error {
	var in struct {
		WeeklySummary *bool `json:"weekly_summary"`
		Onboarding    *bool `json:"onboarding"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	p, err := h.lifecycle.SetPreferences(c.Request().Context(), currentUser(c).ID, in.WeeklySummary, in.Onboarding)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"email_preferences": p})
}
