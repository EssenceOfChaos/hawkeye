package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/statuspage"
)

// publicStatus serves the public status page. No authentication, no cookies: anything here is visible to the whole internet.
func (h *handlers) publicStatus(c echo.Context) error {
	p, err := h.statusPages.Public(c.Request().Context(), c.Param("slug"))
	if err != nil {
		return mapError(err)
	}
	c.Response().Header().Set("Cache-Control", "public, max-age=30")
	return c.JSON(http.StatusOK, p)
}

func (h *handlers) getStatusPage(c echo.Context) error {
	cfg, err := h.statusPages.Get(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"status_page": cfg})
}

func (h *handlers) putStatusPage(c echo.Context) error {
	var in statuspage.Config
	if err := bind(c, &in); err != nil {
		return err
	}
	cfg, err := h.statusPages.Put(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"status_page": cfg})
}

func (h *handlers) deleteStatusPage(c echo.Context) error {
	if err := h.statusPages.Delete(c.Request().Context(), currentOrg(c)); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
