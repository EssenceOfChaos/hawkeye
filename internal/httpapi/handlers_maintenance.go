package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/maintenance"
)

func (h *handlers) listMaintenance(c *echo.Context) error {
	list, err := h.maintenance.List(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"maintenance_windows": list, "max": maintenance.MaxWindowsPerOrganization})
}

func (h *handlers) createMaintenance(c *echo.Context) error {
	var in maintenance.Input
	if err := bind(c, &in); err != nil {
		return err
	}
	w, err := h.maintenance.Create(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.MaintenanceScheduled, target{"maintenance_window", w.ID.String(), w.Name}, w.LogDetails())
	return c.JSON(http.StatusCreated, map[string]any{"maintenance_window": w})
}

func (h *handlers) deleteMaintenance(c *echo.Context) error {
	id, err := uuid.Parse(c.Param("windowID"))
	if err != nil {
		return mapError(maintenance.ErrNotFound)
	}
	name := h.nameOf(c, `SELECT name FROM maintenance_windows WHERE id = $1 AND organization_id = $2`, id)
	if err := h.maintenance.Delete(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.MaintenanceDeleted, target{"maintenance_window", id.String(), name}, nil)
	return c.NoContent(http.StatusNoContent)
}
