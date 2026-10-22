package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/sitecheck"
	"hawkeye/internal/sslcheck"
)

// sslCheck is the free SSL certificate checker: anyone may ask for the certificate a public host presents on port 443. It is a POST so a
// page, crawler or prefetch cannot trigger a connection to a third party just by being loaded. The per-IP limits are applied by middleware
// in front of it; the per-host limit, the concurrency cap and the cache are inside sslcheck. It never returns anything the host sent
// except what is in its certificate, and it does not persist the host that was asked about.
func (h *handlers) sslCheck(c echo.Context) error {
	if h.sslcheck == nil {
		return &APIError{Status: http.StatusServiceUnavailable, Code: "TOOL_UNAVAILABLE", Message: "The SSL checker is not available right now."}
	}
	var in struct {
		Host string `json:"host"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	start := time.Now()
	report, err := h.sslcheck.Inspect(ctx, in.Host)
	if err != nil {
		return h.toolFailed(c, "ssl check", in.Host, start, err)
	}
	h.log.InfoContext(ctx, "ssl check", "outcome", report.Status, "duration_ms", time.Since(start).Milliseconds())
	return c.JSON(http.StatusOK, report)
}

// toolFailed logs a failed check (its outcome and duration; the host only at debug level, as it is not needed to run the service) and returns
// what the visitor is told. An error that is not one of the expected ones is returned as it is, so the API's error handler logs and reports it.
func (h *handlers) toolFailed(c echo.Context, what, host string, start time.Time, err error) error {
	ctx := c.Request().Context()
	out := toolError(c, err)
	var apiErr *APIError
	if !errors.As(out, &apiErr) {
		return out
	}
	h.log.InfoContext(ctx, what, "outcome", apiErr.Code, "duration_ms", time.Since(start).Milliseconds())
	h.log.DebugContext(ctx, what+" host", "host", host, "outcome", apiErr.Code)
	return apiErr
}

// toolError turns an error from a free tool's checker into a message a visitor can act on. Statuses: 400 the input is not a checkable name;
// 422 the host is fine as input but cannot be checked (not found, no HTTPS, not allowed); 429/503/504 limits and timeouts. Anything unexpected
// is returned unchanged.
func toolError(c echo.Context, err error) error {
	var invalid *sslcheck.InvalidHostError
	var limited *sslcheck.TargetLimitedError
	switch {
	case errors.As(err, &invalid):
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_HOST", Message: invalid.Reason}
	case errors.As(err, &limited):
		secs := int(limited.RetryAfter/time.Second) + 1
		c.Response().Header().Set("Retry-After", strconv.Itoa(secs))
		return &APIError{Status: http.StatusTooManyRequests, Code: "HOST_RATE_LIMITED",
			Message: "That domain has been checked several times recently. Please try again in " + waitText(limited.RetryAfter) + "."}
	case errors.Is(err, sslcheck.ErrBlocked), errors.Is(err, sitecheck.ErrBlocked):
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "HOST_NOT_ALLOWED", Message: "We can only check publicly reachable websites."}
	case errors.Is(err, sslcheck.ErrNotFound):
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "HOST_NOT_FOUND", Message: "We could not find that domain. Check the spelling and try again."}
	case errors.Is(err, sslcheck.ErrConnect):
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "CONNECT_FAILED", Message: "We could not connect to that domain on port 443. The server may be down, or it may not offer HTTPS."}
	case errors.Is(err, sslcheck.ErrNoTLS):
		return &APIError{Status: http.StatusUnprocessableEntity, Code: "NO_TLS", Message: "The server answered on port 443 but did not complete a secure (TLS) connection."}
	case errors.Is(err, sslcheck.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return &APIError{Status: http.StatusGatewayTimeout, Code: "HOST_TIMEOUT", Message: "The server did not answer in time."}
	case errors.Is(err, context.Canceled):
		// The visitor left before the answer was ready: nothing will read this, and it is not an error worth reporting.
		return &APIError{Status: http.StatusRequestTimeout, Code: "REQUEST_CANCELLED", Message: "The request was cancelled."}
	case errors.Is(err, sslcheck.ErrBusy):
		c.Response().Header().Set("Retry-After", "5")
		return &APIError{Status: http.StatusServiceUnavailable, Code: "BUSY", Message: "The checker is busy right now. Please try again in a few seconds."}
	}
	return err
}

// waitText is a wait as a visitor reads it: "a minute", "8 minutes".
func waitText(d time.Duration) string {
	m := int((d + time.Minute - 1) / time.Minute)
	if m <= 1 {
		return "a minute"
	}
	return strconv.Itoa(m) + " minutes"
}

// siteCheck is the free website status checker ("is it down?"): anyone may ask whether a public site answers, from our server. The same
// protections as sslCheck apply (POST with CSRF, per-IP limits in middleware, per-host limit, concurrency cap and short cache inside
// sitecheck). Only the host the visitor typed is used, the request is always GET /, and nothing from the response body is returned.
func (h *handlers) siteCheck(c echo.Context) error {
	if h.sitecheck == nil {
		return &APIError{Status: http.StatusServiceUnavailable, Code: "TOOL_UNAVAILABLE", Message: "The website checker is not available right now."}
	}
	var in struct {
		Host string `json:"host"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	start := time.Now()
	report, err := h.sitecheck.Inspect(ctx, in.Host)
	if err != nil {
		return h.toolFailed(c, "site check", in.Host, start, err)
	}
	h.log.InfoContext(ctx, "site check", "outcome", report.Verdict+"/"+report.Reason, "duration_ms", time.Since(start).Milliseconds())
	return c.JSON(http.StatusOK, report)
}
