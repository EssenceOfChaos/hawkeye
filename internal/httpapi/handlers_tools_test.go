package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/sitecheck"
	"hawkeye/internal/sslcheck"
)

func TestToolErrorMapping(t *testing.T) {
	e := echo.New()
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&sslcheck.InvalidHostError{Reason: "nope"}, 400, "INVALID_HOST"},
		{&sitecheck.InvalidHostError{Reason: "nope"}, 400, "INVALID_HOST"},
		{&sslcheck.TargetLimitedError{RetryAfter: 90 * time.Second}, 429, "HOST_RATE_LIMITED"},
		{sslcheck.ErrBlocked, 422, "HOST_NOT_ALLOWED"},
		{sitecheck.ErrBlocked, 422, "HOST_NOT_ALLOWED"},
		{fmt.Errorf("wrapped: %w", sslcheck.ErrNotFound), 422, "HOST_NOT_FOUND"},
		{sslcheck.ErrConnect, 422, "CONNECT_FAILED"},
		{sslcheck.ErrNoTLS, 422, "NO_TLS"},
		{sslcheck.ErrTimeout, 504, "HOST_TIMEOUT"},
		{context.DeadlineExceeded, 504, "HOST_TIMEOUT"},
		{context.Canceled, 408, "REQUEST_CANCELLED"},
		{sslcheck.ErrBusy, 503, "BUSY"},
		{sitecheck.ErrBusy, 503, "BUSY"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		got := toolError(e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec), tc.err)
		var api *APIError
		if !errors.As(got, &api) || api.Status != tc.status || api.Code != tc.code {
			t.Errorf("%v: got %#v, want %d %s", tc.err, got, tc.status, tc.code)
		}
	}
}

func TestToolErrorHeaders(t *testing.T) {
	e := echo.New()
	rec := httptest.NewRecorder()
	toolError(e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec), &sslcheck.TargetLimitedError{RetryAfter: 90 * time.Second})
	if rec.Header().Get("Retry-After") != "91" {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	rec = httptest.NewRecorder()
	toolError(e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec), sslcheck.ErrBusy)
	if rec.Header().Get("Retry-After") != "5" {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

// An error the mapping does not know must reach the API's error handler unchanged, so it is logged and reported and the visitor gets a
// generic message, never the raw error text.
func TestAnUnexpectedToolErrorIsNotTranslated(t *testing.T) {
	e := echo.New()
	boom := errors.New("pq: connection to 10.0.0.5 refused")
	if got := toolError(e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder()), boom); got != boom {
		t.Errorf("got %#v", got)
	}
}

func TestWaitText(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Second: "a minute", time.Minute: "a minute", 61 * time.Second: "2 minutes", 8*time.Minute + time.Second: "9 minutes", 10 * time.Minute: "10 minutes"} {
		if got := waitText(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
