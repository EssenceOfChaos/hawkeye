package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/badges"
)

// enableBadge turns on a monitor's public uptime badge (the owner's choice, as it publishes the monitor's uptime) and returns its token.
func (h *handlers) enableBadge(c *echo.Context) error {
	id, err := h.monitorInScope(c)
	if err != nil {
		return err
	}
	token, err := h.badges.Enable(c.Request().Context(), currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"badge_token": token})
}

func (h *handlers) disableBadge(c *echo.Context) error {
	id, err := h.monitorInScope(c)
	if err != nil {
		return err
	}
	if err := h.badges.Disable(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// publicBadge serves /badge/<token>.svg (?style=small for the README size) to anyone. It is an image only: no script can run in it, and it must
// never be treated as anything else.
func (h *handlers) publicBadge(c *echo.Context) error {
	token, ok := strings.CutSuffix(c.Param("file"), ".svg")
	if !ok {
		return c.NoContent(http.StatusNotFound)
	}
	svg, err := h.badges.Badge(c.Request().Context(), token, c.QueryParam("style"))
	if errors.Is(err, badges.ErrNotFound) {
		return c.NoContent(http.StatusNotFound)
	}
	if err != nil {
		return err
	}
	hdr := c.Response().Header()
	// Five minutes: fresh enough for a status badge, and image proxies such as GitHub's (camo) honor it.
	hdr.Set("Cache-Control", "public, max-age=300")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	hdr.Set("Cross-Origin-Resource-Policy", "cross-origin") // embedded on other sites by design
	return c.Blob(http.StatusOK, "image/svg+xml; charset=utf-8", svg)
}
