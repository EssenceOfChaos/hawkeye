package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"hawkeye/internal/maintenance"
)

func (h *handlers) listMaintenance(c echo.Context) error {
	list, err := h.maintenance.List(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"maintenance_windows": list, "max": maintenance.MaxWindowsPerOrganization})
}

func (h *handlers) createMaintenance(c echo.Context) error {
	var in maintenance.Input
	if err := bind(c, &in); err != nil {
		return err
	}
	w, err := h.maintenance.Create(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"maintenance_window": w})
}

func (h *handlers) deleteMaintenance(c echo.Context) error {
	id, err := uuid.Parse(c.Param("windowID"))
	if err != nil {
		return mapError(maintenance.ErrNotFound)
	}
	if err := h.maintenance.Delete(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
