package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"hawkeye/internal/branding"
	"hawkeye/internal/statuspage"
	"hawkeye/internal/validation"
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

// putStatusPageLogo takes the image itself as the request body (not a form), like the report logo.
func (h *handlers) putStatusPageLogo(c echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, branding.MaxLogoBytes+1))
	if err != nil {
		return mapError(validation.New("logo", "is too large: use an image under 512 KB"))
	}
	if err := h.statusPages.SaveLogo(c.Request().Context(), currentOrg(c), id, raw); err != nil {
		return mapError(err)
	}
	return h.getStatusPage(c)
}

func (h *handlers) deleteStatusPageLogo(c echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	if err := h.statusPages.DeleteLogo(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	return h.getStatusPage(c)
}

// getStatusPageLogo is the owner's preview, shown whatever the plan.
func (h *handlers) getStatusPageLogo(c echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	data, ctype, err := h.statusPages.Logo(c.Request().Context(), currentOrg(c), id)
	return h.sendLogo(c, data, ctype, err, "private, no-cache")
}

// publicStatusLogo serves a published page's logo to anyone. Its URL carries a version (?v=…) that changes with the logo, so it can be cached.
func (h *handlers) publicStatusLogo(c echo.Context) error {
	data, ctype, err := h.statusPages.PublicLogo(c.Request().Context(), c.Param("slug"))
	return h.sendLogo(c, data, ctype, err, "public, max-age=3600")
}

func (h *handlers) sendLogo(c echo.Context, data []byte, ctype string, err error, cache string) error {
	if errors.Is(err, statuspage.ErrNoLogo) {
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "No logo."}
	}
	if err != nil {
		return err
	}
	c.Response().Header().Set("Cache-Control", cache)
	// The stored file was re-encoded from PNG or JPEG, but say so anyway: a browser must never treat it as anything else.
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	c.Response().Header().Set("Content-Security-Policy", "default-src 'none'")
	return c.Blob(http.StatusOK, ctype, data)
}
