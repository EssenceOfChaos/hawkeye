package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
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
	ctx := c.Request().Context()
	// Names first, so deleted monitors can still be named in the log.
	names, err := h.monitorNames(c, in.MonitorIDs)
	if err != nil {
		return err
	}
	n, err := h.monitors.Bulk(ctx, currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	action, details := audit.MonitorChanged, map[string]any{"bulk": in.Action}
	switch in.Action {
	case monitors.BulkPause:
		action = audit.MonitorPaused
	case monitors.BulkResume:
		action = audit.MonitorResumed
	case monitors.BulkDelete:
		action = audit.MonitorDeleted
	}
	for id, name := range names {
		h.recordOrg(c, action, target{"monitor", id.String(), name}, details)
	}
	return c.JSON(http.StatusOK, map[string]any{"updated": n})
}

// monitorNames maps the organization's monitors among ids to their names (unknown and deleted ids are left out).
func (h *handlers) monitorNames(c *echo.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := h.db.Query(c.Request().Context(), `SELECT id, name FROM monitors WHERE organization_id = $1 AND id = ANY($2) AND deleted_at IS NULL`,
		currentOrg(c), ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var (
			id   uuid.UUID
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}
