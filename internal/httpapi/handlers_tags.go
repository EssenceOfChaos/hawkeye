package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/monitors"
)

func tagID(c *echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("tagID"))
	if err != nil {
		return uuid.Nil, mapError(monitors.ErrTagNotFound)
	}
	return id, nil
}

func (h *handlers) listTags(c *echo.Context) error {
	list, err := h.monitors.ListTags(c.Request().Context(), currentOrg(c))
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"tags": list})
}

func (h *handlers) createTag(c *echo.Context) error {
	var in monitors.TagInput
	if err := bind(c, &in); err != nil {
		return err
	}
	t, err := h.monitors.CreateTag(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"tag": t})
}

func (h *handlers) updateTag(c *echo.Context) error {
	id, err := tagID(c)
	if err != nil {
		return err
	}
	var in monitors.TagUpdate
	if err := bind(c, &in); err != nil {
		return err
	}
	t, err := h.monitors.UpdateTag(c.Request().Context(), currentOrg(c), id, in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"tag": t})
}

func (h *handlers) deleteTag(c *echo.Context) error {
	id, err := tagID(c)
	if err != nil {
		return err
	}
	if err := h.monitors.DeleteTag(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) bulkMonitors(c *echo.Context) error {
	var in monitors.BulkInput
	if err := bind(c, &in); err != nil {
		return err
	}
	n, err := h.monitors.Bulk(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"updated": n})
}
