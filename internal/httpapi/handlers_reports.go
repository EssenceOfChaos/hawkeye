package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"hawkeye/internal/incidents"
	"hawkeye/internal/reports"
	"hawkeye/internal/validation"
)

// resolveRange reads range / from / to and applies the organization's plan retention.
func (h *handlers) resolveRange(c echo.Context) (reports.Range, error) {
	bad := map[string]string{}
	parse := func(name string) *time.Time {
		v := c.QueryParam(name)
		if v == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			bad[name] = "must be an RFC 3339 timestamp"
			return nil
		}
		return &t
	}
	from, to := parse("from"), parse("to")
	if len(bad) > 0 {
		return reports.Range{}, mapError(&validation.Error{Fields: bad})
	}
	days, err := h.plans.GetHistoryRetention(c.Request().Context(), h.pool, currentOrg(c))
	if err != nil {
		return reports.Range{}, err
	}
	r, err := reports.ResolveRange(time.Now(), c.QueryParam("range"), from, to, days)
	if err != nil {
		return r, mapError(err)
	}
	return r, nil
}

func rangeJSON(r reports.Range) map[string]any {
	return map[string]any{"from": r.From, "to": r.To, "preset": r.Preset, "clamped_to_retention": r.Clamped, "retention_days": r.RetentionDays}
}

// monitorInScope checks the monitor belongs to the caller's organization (404 otherwise).
func (h *handlers) monitorInScope(c echo.Context) (uuid.UUID, error) {
	id, err := monitorID(c)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := h.monitors.Get(c.Request().Context(), currentOrg(c), id); err != nil {
		return uuid.Nil, mapError(err)
	}
	return id, nil
}

func (h *handlers) monitorUptime(c echo.Context) error {
	id, err := h.monitorInScope(c)
	if err != nil {
		return err
	}
	r, err := h.resolveRange(c)
	if err != nil {
		return err
	}
	u, err := reports.ComputeUptime(c.Request().Context(), h.db, currentOrg(c), &id, r)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"range": rangeJSON(r), "uptime": u})
}

func (h *handlers) monitorResponseTimes(c echo.Context) error {
	id, err := h.monitorInScope(c)
	if err != nil {
		return err
	}
	return h.series(c, &id)
}

func (h *handlers) dashboardResponseTimes(c echo.Context) error { return h.series(c, nil) }

func (h *handlers) series(c echo.Context, monitor *uuid.UUID) error {
	r, err := h.resolveRange(c)
	if err != nil {
		return err
	}
	pts, bucket, err := reports.Series(c.Request().Context(), h.db, currentOrg(c), monitor, r)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"range": rangeJSON(r), "bucket_seconds": bucket, "points": pts})
}

func (h *handlers) monitorResults(c echo.Context) error {
	id, err := h.monitorInScope(c)
	if err != nil {
		return err
	}
	r, err := h.resolveRange(c)
	if err != nil {
		return err
	}
	limit, offset, bad := 50, 0, map[string]string{}
	if v := c.QueryParam("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			bad["limit"] = "must be between 1 and 200"
		}
		limit = n
	}
	if v := c.QueryParam("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			bad["offset"] = "must be 0 or greater"
		}
		offset = n
	}
	if len(bad) > 0 {
		return mapError(&validation.Error{Fields: bad})
	}
	list, total, err := reports.Results(c.Request().Context(), h.db, currentOrg(c), id, r, limit, offset)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"range": rangeJSON(r), "results": list, "total": total, "limit": limit, "offset": offset})
}

// dashboardSummary: monitor counts and open incidents describe the present; uptime and response figures are
// scoped to the requested range (default 24h), so the dashboard's filter row governs every number on it.
func (h *handlers) dashboardSummary(c echo.Context) error {
	r, err := h.resolveRange(c)
	if err != nil {
		return err
	}
	s, err := reports.DashboardSummary(c.Request().Context(), h.db, currentOrg(c), r, time.Now())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"range": rangeJSON(r), "summary": s})
}

func (h *handlers) dashboardRecentIncidents(c echo.Context) error {
	limit := 5
	if v := c.QueryParam("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 20 {
			return mapError(validation.New("limit", "must be between 1 and 20"))
		}
		limit = n
	}
	list, _, err := incidents.List(c.Request().Context(), h.db, currentOrg(c), incidents.Filter{Limit: limit})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"incidents": list})
}
