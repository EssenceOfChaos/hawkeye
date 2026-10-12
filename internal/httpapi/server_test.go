package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/httpapi"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newApp(db httpapi.Pinger) http.Handler {
	return httpapi.New(httpapi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: db,
		RequestTimeout: 5 * time.Second, BodyLimit: "1K",
	})
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := do(newApp(fakeDB{}), http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestHealthzIgnoresDatabase(t *testing.T) {
	if rec := do(newApp(fakeDB{errors.New("down")}), http.MethodGet, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("liveness must not depend on the database, got %d", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	if rec := do(newApp(fakeDB{}), http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}

	rec := do(newApp(fakeDB{errors.New("dial tcp 10.0.0.5:5432: refused")}), http.MethodGet, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatal("readiness response leaks internal details")
	}
}

func TestNotFoundUsesErrorEnvelope(t *testing.T) {
	rec := do(newApp(fakeDB{}), http.MethodGet, "/nope")
	var body httpapi.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound || body.Error.Code != "NOT_FOUND" {
		t.Fatalf("got %d %+v", rec.Code, body)
	}
}

func TestRequestIDAndSecurityHeaders(t *testing.T) {
	rec := do(newApp(fakeDB{}), http.MethodGet, "/healthz")
	if rec.Header().Get("X-Request-Id") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing headers: %v", rec.Header())
	}
}
