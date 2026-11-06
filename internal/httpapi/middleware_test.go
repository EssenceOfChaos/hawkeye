package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"hawkeye/internal/metrics"
)

type captured struct {
	errs   []error
	fields []map[string]string
}

func (r *captured) Capture(_ context.Context, err error, fields map[string]string) {
	r.errs, r.fields = append(r.errs, err), append(r.fields, fields)
}

func panicking(t *testing.T, handler echo.HandlerFunc) (*httptest.ResponseRecorder, *metrics.APIMetrics, *captured, *strings.Builder) {
	t.Helper()
	m, rep, logs := metrics.NewAPI(), &captured{}, &strings.Builder{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	e := echo.New()
	e.HTTPErrorHandler = errorHandler(log, rep)
	e.Use(recoverPanics(log, m, rep))
	e.GET("/x", handler)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec, m, rep, logs
}

func TestRecoverPanicsReportsOnceAndHidesTheDetail(t *testing.T) {
	cause := errors.New("database password=hunter2")
	for name, value := range map[string]any{"an error": cause, "a string": "boom: secret"} {
		t.Run(name, func(t *testing.T) {
			rec, m, rep, logs := panicking(t, func(*echo.Context) error { panic(value) })
			if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "INTERNAL_ERROR") ||
				strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "secret") {
				t.Fatalf("got %d %s", rec.Code, rec.Body)
			}
			if got := testutil.ToFloat64(m.Panics); got != 1 {
				t.Fatalf("panics counted = %v", got)
			}
			if len(rep.errs) != 1 || rep.fields[0]["kind"] != "panic" || rep.fields[0]["route"] != "/x" {
				t.Fatalf("a panic must be reported once, as a panic: %v %v", rep.errs, rep.fields)
			}
			if err, ok := value.(error); ok && !errors.Is(rep.errs[0], err) {
				t.Fatalf("the report lost the original error: %v", rep.errs[0])
			}
			if !strings.Contains(logs.String(), "panic recovered") || !strings.Contains(logs.String(), "middleware_test.go") {
				t.Fatalf("the log needs a stack that reaches the handler: %s", logs)
			}
		})
	}
}

// net/http uses this panic to abort a response on purpose, so it must reach the server rather than become a 500 and a false report.
func TestRecoverPanicsLeavesAbortHandlerAlone(t *testing.T) {
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Fatalf("recovered %v", r)
		}
	}()
	panicking(t, func(*echo.Context) error { panic(http.ErrAbortHandler) })
	t.Fatal("the panic did not get through")
}

func TestRecoverPanicsPassesOrdinaryErrorsThrough(t *testing.T) {
	rec, m, rep, _ := panicking(t, func(*echo.Context) error { return &APIError{Status: http.StatusConflict, Code: "TAKEN", Message: "x"} })
	if rec.Code != http.StatusConflict || testutil.ToFloat64(m.Panics) != 0 || len(rep.errs) != 0 {
		t.Fatalf("got %d, panics %v, reports %v", rec.Code, testutil.ToFloat64(m.Panics), rep.errs)
	}
}

// The metrics middleware sees errors the error handler has not rendered yet, so it has to work out the same status the client will get.
func TestAPIMetricsRecordTheStatusOfAnUnrenderedError(t *testing.T) {
	cases := []struct {
		name    string
		handler echo.HandlerFunc
		want    string
	}{
		{"api error", func(*echo.Context) error { return &APIError{Status: http.StatusConflict, Code: "TAKEN"} }, "409"},
		{"http error", func(*echo.Context) error { return echo.NewHTTPError(http.StatusTeapot, "x") }, "418"},
		{"echo's own error", func(*echo.Context) error { return echo.ErrMethodNotAllowed }, "405"},
		{"unknown error", func(*echo.Context) error { return errors.New("broken") }, "500"},
		{"no error", func(c *echo.Context) error { return c.NoContent(http.StatusAccepted) }, "202"},
		{"error after the response started", func(c *echo.Context) error {
			if err := c.NoContent(http.StatusCreated); err != nil {
				return err
			}
			return errors.New("too late")
		}, "201"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := metrics.NewAPI()
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			e := echo.New()
			e.HTTPErrorHandler = errorHandler(log, &captured{})
			e.Use(apiMetrics(m))
			e.GET("/x", tc.handler)
			e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

			fams, err := m.Registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			var seen []string
			for _, f := range fams {
				if f.GetName() != "hawkeye_http_requests_total" {
					continue
				}
				for _, metric := range f.GetMetric() {
					for _, l := range metric.GetLabel() {
						if l.GetName() == "code" {
							seen = append(seen, l.GetValue())
						}
					}
				}
			}
			if len(seen) != 1 || seen[0] != tc.want {
				t.Fatalf("recorded %v, want %s", seen, tc.want)
			}
		})
	}
}
