package worker_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"hawkeye/internal/checks"
	"hawkeye/internal/domains"
	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
)

// A workflow saved through the monitors service (so its secrets are sealed) is run by the worker like any monitor: two failed runs confirm an
// outage (incidents follow from the state exactly as for other monitors), the failing step is named, and each run's steps are stored without values.
func TestWorkerRunsSavedWorkflows(t *testing.T) {
	e := newEnv(t)
	var broken atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"hunter2-secret"`) {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"token":"tok-0123456789"}`)
		case "/orders":
			if broken.Load() || r.Header.Get("Authorization") != "Bearer tok-0123456789" {
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"orders":[]}`)
		}
	}))
	defer srv.Close()

	svc := monitors.NewService(e.pool, plans.NewService("http://app.test"), monitors.WithSecretBox(testutil.SecretBox()), monitors.WithDomainSync(domains.SyncTx))
	pw := "hunter2-secret"
	body := `{"password":"{{secret.pw}}"}`
	interval := 300
	m, err := svc.Create(t.Context(), e.org, monitors.CreateInput{Type: monitors.TypeWorkflow, Name: "Checkout", CheckIntervalSeconds: &interval,
		Secrets: map[string]*string{"pw": &pw},
		Steps: []monitors.StepInput{
			{Name: "Sign in", Method: "POST", URL: "https://api.example.com/login", Body: &body,
				Extractions: []checks.Extraction{{Name: "token", From: "json_path", Path: "token"}}},
			{Name: "List orders", Method: "GET", URL: "https://api.example.com/orders", Auth: &monitors.AuthInput{Type: "bearer", Secret: strp("{{token}}")}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	// Saved with a public address (monitors only take ports 80 and 443), then pointed at the local test server.
	if _, err := e.pool.Exec(t.Context(), `UPDATE monitor_steps SET url = replace(url, 'https://api.example.com', $2) WHERE monitor_id = $1`, m.ID, srv.URL); err != nil {
		t.Fatal(err)
	}
	var sealed string
	e.pool.QueryRow(t.Context(), `SELECT value FROM monitor_secrets WHERE monitor_id = $1`, m.ID).Scan(&sealed)
	if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, pw) {
		t.Fatalf("the secret is stored as %q", sealed)
	}

	w := e.worker(loopbackCfg())
	e.round(t, w)
	if s := e.state(t, m.ID); s.Successes != 1 || s.Failures != 0 {
		t.Fatalf("after a good run: %+v", s)
	}
	var steps []map[string]any
	var raw []byte
	e.pool.QueryRow(t.Context(), `SELECT steps FROM check_results WHERE monitor_id = $1 ORDER BY id DESC LIMIT 1`, m.ID).Scan(&raw)
	if err := json.Unmarshal(raw, &steps); err != nil || len(steps) != 2 || steps[1]["name"] != "List orders" || steps[1]["success"] != true {
		t.Fatalf("stored steps %s", raw)
	}
	if strings.Contains(string(raw), "tok-0123456789") || strings.Contains(string(raw), pw) {
		t.Fatalf("a value was stored: %s", raw)
	}

	broken.Store(true)
	e.round(t, w)
	e.round(t, w)
	s := e.state(t, m.ID)
	var summary string
	e.pool.QueryRow(t.Context(), `SELECT error_summary FROM check_results WHERE monitor_id = $1 ORDER BY id DESC LIMIT 1`, m.ID).Scan(&summary)
	if s.Status != "down" || summary != "Step 2 (List orders): Received HTTP 503" {
		t.Fatalf("after two failed runs: %+v, %q", s, summary)
	}
}

func strp(s string) *string { return &s }
