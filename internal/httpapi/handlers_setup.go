package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/discovery"
	"hawkeye/internal/monitors"
)

// suggestMonitors is "set up from your domain": it looks at a domain the organization names and proposes monitors for it, marking those the
// organization already has. Nothing is created here; the person picks from the list and the app creates each one through POST /monitors, so plan
// limits and validation are exactly those of adding a monitor by hand. Requests to the site go through internal/discovery's SSRF-safe client and
// shared limits, with per-IP and per-organization budgets in front (server.go). The domain is not stored.
func (h *handlers) suggestMonitors(c *echo.Context) error {
	if h.discovery == nil {
		return &APIError{Status: http.StatusServiceUnavailable, Code: "SETUP_UNAVAILABLE", Message: "Setting up from a domain is not available right now."}
	}
	var in struct {
		Domain string `json:"domain"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	start := time.Now()
	report, err := h.discovery.Suggest(ctx, in.Domain)
	if err != nil {
		return h.toolFailed(c, "monitor suggestions", in.Domain, start, err)
	}
	// The report may be shared with other organizations through the cache, so it is copied before this organization's details are added.
	report.Suggestions = append([]discovery.Suggestion(nil), report.Suggestions...)
	proposed := make([]monitors.Target, len(report.Suggestions))
	for i, s := range report.Suggestions {
		proposed[i] = monitors.Target{Type: s.Monitor.Type, URL: s.Monitor.URL}
		if s.Monitor.DNSRecordType != nil {
			proposed[i].DNSRecordType = *s.Monitor.DNSRecordType
		}
	}
	covered, err := h.monitors.Covers(ctx, currentOrg(c), proposed)
	if err != nil {
		return err
	}
	for i := range report.Suggestions {
		report.Suggestions[i].AlreadyMonitored = covered[i]
	}
	h.log.InfoContext(ctx, "monitor suggestions", "count", len(report.Suggestions), "duration_ms", time.Since(start).Milliseconds())
	return c.JSON(http.StatusOK, report)
}

// setupError covers the errors only setup returns; the rest are the free tools' (toolError).
func setupError(err error) *APIError {
	switch {
	case errors.Is(err, discovery.ErrNotFound):
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "HOST_NOT_FOUND", Message: "We could not find that domain. Check the spelling and try again."}
	case errors.Is(err, discovery.ErrBlocked):
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "HOST_NOT_ALLOWED", Message: "We can only set up publicly reachable websites."}
	}
	return nil
}
