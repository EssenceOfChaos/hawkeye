package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/regions"
)

// listRegions returns the locations checks can run from and how many the organization's plan lets one monitor use.
func (h *handlers) listRegions(c echo.Context) error {
	p, err := h.plans.Plan(c.Request().Context(), h.pool, currentOrg(c))
	if err != nil {
		return err
	}
	set := h.regions
	if len(set.List()) == 0 {
		set = regions.Default()
	}
	return c.JSON(http.StatusOK, map[string]any{"regions": set.List(), "primary": set.Primary(), "max_regions": min(p.MaxRegions, max(1, len(set.List())))})
}

// monitorRegions returns how each of a monitor's regions currently sees it.
func (h *handlers) monitorRegions(c echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	list, err := h.monitors.RegionStatuses(c.Request().Context(), currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"regions": list})
}
