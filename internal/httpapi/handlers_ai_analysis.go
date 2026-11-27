package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/incidentanalysis"
	"hawkeye/internal/incidents"
)

// AI incident analysis (internal/incidentanalysis, docs/plan-ai-incident-analysis.md): once the owner turns it on, the worker writes the likely cause
// of each confirmed outage from its failure snapshot, counted against the plan's monthly allowance. Off for everyone until the owner turns it on;
// off entirely when the server has no ANTHROPIC_API_KEY or AI_ANALYSIS_ENABLED=false.

var errAnalysisOff = &APIError{Status: http.StatusConflict, Code: "AI_ANALYSIS_OFF",
	Message: "AI incident analysis is turned off for this organization. The owner can turn it on from the Incidents page."}

var errAnalysisUnavailable = &APIError{Status: http.StatusServiceUnavailable, Code: "AI_ANALYSIS_UNAVAILABLE",
	Message: "AI incident analysis is not available right now. Try again later."}

// aiAnalysisSetting tells the app whether to offer analysis, and how much of the month's allowance is used.
func (h *handlers) aiAnalysisSetting(c *echo.Context) error {
	ctx := c.Request().Context()
	p, err := h.plans.Plan(ctx, h.pool, currentOrg(c))
	if err != nil {
		return err
	}
	on, err := h.orgs.AIAnalysisEnabled(ctx, currentOrg(c))
	if err != nil {
		return mapError(err)
	}
	used, err := incidentanalysis.UsedThisMonth(ctx, h.db, currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"available": h.aiAnalysis, "included": p.MaxAIAnalyses > 0, "enabled": on,
		"limit": p.MaxAIAnalyses, "used": used, "model": incidentanalysis.ModelName})
}

func (h *handlers) setAIAnalysis(c *echo.Context) error {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	if in.Enabled {
		if !h.aiAnalysis {
			return errAnalysisUnavailable
		}
		if err := h.plans.CanUseAIAnalysis(ctx, h.pool, currentOrg(c)); err != nil {
			return mapError(err)
		}
	}
	if err := h.orgs.SetAIAnalysis(ctx, currentOrg(c), in.Enabled); err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.AIAnalysisSetting, target{}, map[string]any{"enabled": in.Enabled})
	return h.aiAnalysisSetting(c)
}

// incidentAnalysis returns an incident's analysis: 404 when it has none.
func (h *handlers) incidentAnalysis(c *echo.Context) error {
	id, err := incidentID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if _, err := incidents.Get(ctx, h.db, currentOrg(c), id); err != nil {
		return mapError(err)
	}
	a, err := incidentanalysis.Load(ctx, h.db, id)
	if err != nil {
		return err
	}
	if a == nil {
		return echo.NewHTTPError(http.StatusNotFound, "this incident has no analysis")
	}
	return c.JSON(http.StatusOK, map[string]any{"analysis": a})
}

// requestIncidentAnalysis asks for a new analysis of an incident (the first, or one replacing the last). It runs in the background; the app polls
// GET …/analysis until it is no longer pending or running.
func (h *handlers) requestIncidentAnalysis(c *echo.Context) error {
	id, err := incidentID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	org := currentOrg(c)
	if _, err := incidents.Get(ctx, h.db, org, id); err != nil {
		return mapError(err)
	}
	if !h.aiAnalysis {
		return errAnalysisUnavailable
	}
	if err := h.plans.CanUseAIAnalysis(ctx, h.pool, org); err != nil {
		return mapError(err)
	}
	on, err := h.orgs.AIAnalysisEnabled(ctx, org)
	if err != nil {
		return mapError(err)
	}
	if !on {
		return errAnalysisOff
	}
	// Checked here so the person hears at once; the worker checks again under its lock when the run starts.
	p, err := h.plans.Plan(ctx, h.pool, org)
	if err != nil {
		return err
	}
	used, err := incidentanalysis.UsedThisMonth(ctx, h.db, org)
	if err != nil {
		return err
	}
	if used >= p.MaxAIAnalyses {
		return mapError(h.plans.AIAnalysisLimitError(ctx, h.pool, p, used))
	}
	switch err := incidentanalysis.Request(ctx, h.db, org, id); {
	case errors.Is(err, incidentanalysis.ErrNoSnapshot):
		return &APIError{Status: http.StatusConflict, Code: "NO_SNAPSHOT",
			Message: "This incident has no failure snapshot to analyze. Only website, API and multi-step monitors' outages from 2026-11-27 on have one."}
	case errors.Is(err, incidentanalysis.ErrInProgress):
		return &APIError{Status: http.StatusConflict, Code: "ANALYSIS_IN_PROGRESS", Message: "An analysis of this incident is already being written."}
	case err != nil:
		return err
	}
	a, err := incidentanalysis.Load(ctx, h.db, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, map[string]any{"analysis": a})
}
