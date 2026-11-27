package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/incidents"
)

// incidentSnapshot returns what HawkEye saw when the incident's outage was confirmed (docs/plan-failure-snapshots.md), without the body
// excerpt. 404 when the incident isn't the organization's or has no snapshot (heartbeats, DNS monitors, incidents from before snapshots).
func (h *handlers) incidentSnapshot(c *echo.Context) error {
	id, err := incidentID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if _, err := incidents.Get(ctx, h.db, currentOrg(c), id); err != nil {
		return mapError(err)
	}
	s, err := incidents.LoadSnapshot(ctx, h.db, id)
	if err != nil {
		return err
	}
	if s == nil {
		return echo.NewHTTPError(http.StatusNotFound, "this incident has no failure snapshot")
	}
	return c.JSON(http.StatusOK, map[string]any{"snapshot": s})
}

// incidentSnapshotBody returns the start of the failed response's body, for monitors that opted in. Members of the organization can read it,
// like the incident itself. It is response text from the monitored site: returned as JSON data, never cached, never logged.
func (h *handlers) incidentSnapshotBody(c *echo.Context) error {
	id, err := incidentID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if _, err := incidents.Get(ctx, h.db, currentOrg(c), id); err != nil {
		return mapError(err)
	}
	if h.box == nil {
		return echo.NewHTTPError(http.StatusNotFound, "response excerpts are not available on this server")
	}
	e, err := incidents.LoadExcerpt(ctx, h.db, h.box, id)
	if errors.Is(err, incidents.ErrNoExcerpt) {
		return echo.NewHTTPError(http.StatusNotFound, "this incident has no response excerpt")
	}
	if err != nil {
		return err
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]any{"body": e})
}
