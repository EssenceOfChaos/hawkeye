package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/incidentdraft"
	"hawkeye/internal/incidents"
	"hawkeye/internal/monitors"
)

// AI-drafted incident updates: an owner on Pro or Agency who has turned them on can ask for a first draft of an update, which they edit and post as
// any other (POST /incidents/:id/updates). Drafts are written on request, returned, and never stored or posted. Off for everyone until the owner
// turns them on; off entirely when the server has no ANTHROPIC_API_KEY.

var errDraftsOff = &APIError{Status: http.StatusConflict, Code: "AI_DRAFTS_OFF",
	Message: "AI-drafted updates are turned off for this organization. The owner can turn them on from the Incidents page."}

var errDraftsUnavailable = &APIError{Status: http.StatusServiceUnavailable, Code: "AI_DRAFTS_UNAVAILABLE",
	Message: "AI-drafted updates are not available right now. Write the update yourself, or try again later."}

// aiDrafts tells the app whether to offer drafts: available on this server, included in the plan, and turned on by the owner.
func (h *handlers) aiDrafts(c *echo.Context) error {
	ctx := c.Request().Context()
	p, err := h.plans.Plan(ctx, h.pool, currentOrg(c))
	if err != nil {
		return err
	}
	on, err := h.orgs.AIDraftsEnabled(ctx, currentOrg(c))
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"available": h.drafts != nil, "included": p.AIDraftsEnabled, "enabled": on,
		"model": "Claude Haiku 5.5 by Anthropic"})
}

func (h *handlers) setAIDrafts(c *echo.Context) error {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	if in.Enabled {
		if h.drafts == nil {
			return errDraftsUnavailable
		}
		if err := h.plans.CanUseAIDrafts(ctx, h.pool, currentOrg(c)); err != nil {
			return mapError(err)
		}
	}
	if err := h.orgs.SetAIDrafts(ctx, currentOrg(c), in.Enabled); err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.AIDraftsSetting, target{}, map[string]any{"enabled": in.Enabled})
	return h.aiDrafts(c)
}

// draftIncidentUpdate writes a first draft of an update for the status the owner chose. It sends the model only what draftFacts gathers.
func (h *handlers) draftIncidentUpdate(c *echo.Context) error {
	id, err := incidentID(c)
	if err != nil {
		return err
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if !slices.Contains(incidents.UpdateStatuses, in.Status) {
		return &APIError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "One or more fields are invalid.",
			Details: map[string]any{"fields": map[string]string{"status": "must be investigating, identified, monitoring or resolved"}}}
	}
	ctx := c.Request().Context()
	if h.drafts == nil {
		return errDraftsUnavailable
	}
	if err := h.plans.CanUseAIDrafts(ctx, h.pool, currentOrg(c)); err != nil {
		return mapError(err)
	}
	on, err := h.orgs.AIDraftsEnabled(ctx, currentOrg(c))
	if err != nil {
		return mapError(err)
	}
	if !on {
		return errDraftsOff
	}
	facts, err := h.draftFacts(ctx, c, id, in.Status)
	if err != nil {
		return mapError(err)
	}
	start := time.Now()
	text, err := h.drafts.Draft(ctx, facts)
	switch {
	case errors.Is(err, incidentdraft.ErrRefused):
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "DRAFT_DECLINED", Message: "A draft couldn't be written for this incident. Write the update yourself."}
	case errors.Is(err, context.Canceled):
		return &APIError{Status: http.StatusRequestTimeout, Code: "REQUEST_CANCELED", Message: "The request was canceled."}
	case err != nil:
		h.log.WarnContext(ctx, "incident draft failed", "error", err, "duration_ms", time.Since(start).Milliseconds())
		return errDraftsUnavailable
	}
	h.log.InfoContext(ctx, "incident draft", "status", in.Status, "duration_ms", time.Since(start).Milliseconds())
	return c.JSON(http.StatusOK, map[string]any{"message": text})
}

// draftFacts gathers what the draft may be based on: never the monitor's address, its request settings or any response content.
func (h *handlers) draftFacts(ctx context.Context, c *echo.Context, id uuid.UUID, status string) (incidentdraft.Facts, error) {
	org := currentOrg(c)
	inc, err := incidents.Get(ctx, h.db, org, id)
	if err != nil {
		return incidentdraft.Facts{}, err
	}
	kind, codes, regionCodes, err := incidents.Failures(ctx, h.db, org, id)
	if err != nil {
		return incidentdraft.Facts{}, err
	}
	updates, err := incidents.ListUpdates(ctx, h.db, org, id)
	if err != nil {
		return incidentdraft.Facts{}, err
	}
	f := incidentdraft.Facts{MonitorName: inc.MonitorName, MonitorKind: kindWords(kind), Status: status, StartedAt: inc.StartedAt,
		ResolvedAt: inc.ResolvedAt, Ongoing: inc.Status == "open", Duration: (time.Duration(inc.DurationSeconds) * time.Second).String(),
		StatusCodes: codes}
	if inc.CauseCategory != nil {
		f.Cause = *inc.CauseCategory
	}
	if inc.Summary != nil {
		f.Summary = *inc.Summary
	}
	for _, r := range regionCodes {
		f.Locations = append(f.Locations, h.regions.Name(r))
	}
	slices.SortFunc(updates, func(a, b incidents.Update) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for _, u := range updates {
		f.Previous = append(f.Previous, incidentdraft.Posted{Status: u.Status, Message: u.Message, PostedAt: u.CreatedAt})
	}
	return f, nil
}

func kindWords(kind string) string {
	switch kind {
	case monitors.TypeHeartbeat:
		return "scheduled job (cron job)"
	case monitors.TypeDNS:
		return "DNS record"
	case monitors.TypeWorkflow:
		return "multi-step API check"
	}
	return "website or API"
}
