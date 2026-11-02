package httpapi

import (
	"net/http"

	"github.com/google/uuid"
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

func statusPageID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("pageID"))
	if err != nil {
		return uuid.Nil, mapError(statuspage.ErrNotFound)
	}
	return id, nil
}

// listStatusPages returns the organization's pages and how many its plan allows.
func (h *handlers) listStatusPages(c echo.Context) error {
	ctx := c.Request().Context()
	list, err := h.statusPages.List(ctx, currentOrg(c))
	if err != nil {
		return err
	}
	p, err := h.plans.Plan(ctx, h.pool, currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"status_pages": list, "max": p.MaxStatusPages})
}

func (h *handlers) getStatusPage(c echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	cfg, err := h.statusPages.Get(c.Request().Context(), currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"status_page": cfg})
}

func (h *handlers) createStatusPage(c echo.Context) error {
	var in statuspage.Config
	if err := bind(c, &in); err != nil {
		return err
	}
	cfg, err := h.statusPages.Create(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"status_page": cfg})
}

func (h *handlers) updateStatusPage(c echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	var in statuspage.Config
	if err := bind(c, &in); err != nil {
		return err
	}
	cfg, err := h.statusPages.Update(c.Request().Context(), currentOrg(c), id, in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"status_page": cfg})
}

func (h *handlers) deleteStatusPage(c echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	if err := h.statusPages.Delete(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
