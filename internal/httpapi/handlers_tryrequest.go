package httpapi

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/monitors"
)

// testRequest sends an unsaved website or API monitor's request once and returns the response, so rules can be written by clicking its fields,
// and says which of the draft's checks hold. The response goes back to the person who asked and is never stored or logged. Like test runs of
// multi-step checks, it makes our servers send requests on demand: the same SSRF-safe client, and the route requires a verified address and
// has per-IP and per-organization limits.
func (h *handlers) testRequest(c *echo.Context) error {
	if h.workflowChecker == nil {
		return &APIError{Status: http.StatusServiceUnavailable, Code: "UNAVAILABLE", Message: "Testing is not available on this server."}
	}
	var in monitors.RequestDraft
	if err := bind(c, &in); err != nil {
		return err
	}
	t, err := h.monitors.TryRequest(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	// Monitors may wait up to 30 seconds; a test stops at 25, inside the API's own request timeout, as test runs of multi-step checks do.
	ctx, cancel := context.WithTimeout(c.Request().Context(), testWorkflowTimeout)
	defer cancel()
	t.Timeout = min(t.Timeout, testWorkflowTimeout)
	res, p, err := h.workflowChecker.TryRequest(ctx, t)
	if err != nil {
		return err
	}
	out := map[string]any{"result": map[string]any{
		"success": res.Success, "status_code": res.StatusCode, "response_time_ms": res.ResponseTimeMs,
		"error_category": nullIfEmpty(res.ErrorCategory), "error_summary": nullIfEmpty(res.ErrorSummary),
		"content_type": nullIfEmpty(res.ContentType), "final_url": nullIfEmpty(res.FinalURL), "timings": res.Timings,
	}, "response": nil, "checks": []any{}}
	if res.StatusCode != nil {
		pj := previewJSON(p)
		out["checks"] = pj["checks"]
		delete(pj, "checks")
		out["response"] = pj
	}
	// The body may hold anything the site returns: keep it out of shared caches and the browser's disk cache.
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, out)
}
