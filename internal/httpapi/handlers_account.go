package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/ratelimit"
)

func (h *handlers) changePassword(c echo.Context) error {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	cookie, err := c.Cookie(sessionCookie)
	if err != nil {
		return errUnauthenticated
	}
	if err := h.auth.ChangePassword(c.Request().Context(), currentUser(c), cookie.Value, in.CurrentPassword, in.NewPassword); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// exportData returns the user's data as a JSON download. It is rate-limited per user because it reads a lot.
func (h *handlers) exportData(c echo.Context) error {
	if h.limits.l != nil {
		if res, err := h.limits.l.Allow(c.Request().Context(), ratelimit.Key("account-export", currentUser(c).ID.String()), 5, time.Hour); err == nil && !res.Allowed {
			c.Response().Header().Set("Retry-After", "3600")
			return errRateLimited
		}
	}
	data, err := h.account.Export(c.Request().Context(), currentUser(c).ID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	c.Response().Header().Set("Content-Disposition", `attachment; filename="hawkeye-export.json"`)
	return c.Blob(http.StatusOK, "application/json", b)
}

// deleteAccount permanently deletes the account after the password and email are re-confirmed.
func (h *handlers) deleteAccount(c echo.Context) error {
	var in struct {
		Password     string `json:"password"`
		ConfirmEmail string `json:"confirm_email"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := h.account.Delete(c.Request().Context(), currentUser(c), in.Password, in.ConfirmEmail); err != nil {
		return h.accountError(c, err)
	}
	h.clearSessionCookie(c)
	return c.NoContent(http.StatusNoContent)
}
