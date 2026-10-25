package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// unsubscribeLifecycle stops onboarding emails for the person the link's token belongs to. The token comes from the query string, not a body, because
// a mail client's one-click POST (RFC 8058) goes to exactly the URL from the List-Unsubscribe header and sends only "List-Unsubscribe=One-Click".
// It succeeds again for someone already unsubscribed. An unknown token is an error, so the page can say the link is wrong.
func (h *handlers) unsubscribeLifecycle(c echo.Context) error {
	if err := h.lifecycle.Unsubscribe(c.Request().Context(), c.QueryParam("token")); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
