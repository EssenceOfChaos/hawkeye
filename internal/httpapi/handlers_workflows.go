package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/checks"
	"hawkeye/internal/monitors"
)

// testWorkflowTimeout bounds a test run, inside the API's own request timeout. Scheduled runs get the full checks.WorkflowTimeout.
const testWorkflowTimeout = 25 * time.Second

// testWorkflow runs an unsaved multi-step API check once and reports each step, so it can be built and fixed before it is saved. Nothing is
// stored. It makes our servers send requests on demand, so it needs a verified address and has its own per-IP and per-organization limits.
func (h *handlers) testWorkflow(c *echo.Context) error {
	if h.workflowChecker == nil {
		return &APIError{Status: http.StatusServiceUnavailable, Code: "UNAVAILABLE", Message: "Testing is not available on this server."}
	}
	var in monitors.Draft
	if err := bind(c, &in); err != nil {
		return err
	}
	for _, m := range monitors.StepMethods(in.Steps) {
		if err := requireVerifiedForWriteMethod(c, &m); err != nil {
			return err
		}
	}
	wf, err := h.monitors.TryWorkflow(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), testWorkflowTimeout)
	defer cancel()
	res, err := h.workflowChecker.RunWorkflow(ctx, wf)
	if errors.Is(err, context.DeadlineExceeded) {
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "TEST_TOO_SLOW",
			Message: "The test took longer than 25 seconds, so it was stopped. Scheduled checks allow 60 seconds; save it to see how it does."}
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"result": workflowResult(res)})
}

// workflowResult is a run as the API shows it: the outcome, and what each step that ran did.
func workflowResult(r checks.Result) map[string]any {
	return map[string]any{
		"success": r.Success, "status_code": r.StatusCode, "response_time_ms": r.ResponseTimeMs,
		"error_category": nullIfEmpty(r.ErrorCategory), "error_summary": nullIfEmpty(r.ErrorSummary), "steps": r.Steps,
	}
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
