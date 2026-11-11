package statusbeat_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"hawkeye/internal/statusbeat"
	"hawkeye/internal/testutil"
)

func TestHeartbeatReportsHowOverdueChecksAre(t *testing.T) {
	pool := testutil.NewPool(t)
	var got []map[string]any
	var auth string
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer page.Close()
	b := statusbeat.New(pool, page.URL, "secret", "primary", slog.New(slog.NewTextHandler(io.Discard, nil)))

	// An empty queue is nothing overdue.
	if err := b.Send(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A check that should have run 10 minutes ago, and one for another region (not ours to report).
	if _, err := pool.Exec(t.Context(), `WITH o AS (INSERT INTO organizations (name) VALUES ('o') RETURNING id),
		m AS (INSERT INTO monitors (organization_id, name, url, check_interval_seconds) SELECT id, 'm', 'https://example.com', 300 FROM o RETURNING id)
		INSERT INTO check_jobs (monitor_id, region, run_at) SELECT id, r, now() - d FROM m,
			(VALUES ('primary', interval '10 minutes'), ('eu-central', interval '1 hour')) v(r, d)`); err != nil {
		t.Fatal(err)
	}
	if err := b.Send(t.Context()); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret" || len(got) != 2 || got[0]["overdue_seconds"] != float64(0) || got[0]["region"] != "primary" {
		t.Fatalf("auth %q, beats %v", auth, got)
	}
	if o := got[1]["overdue_seconds"].(float64); o < 595 || o > 700 {
		t.Fatalf("overdue %v, want about 600 (the other region's hour doesn't count)", o)
	}
}

func TestHeartbeatSaysWhenThePageRefusesIt(t *testing.T) {
	pool := testutil.NewPool(t)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer page.Close()
	err := statusbeat.New(pool, page.URL, "wrong", "primary", slog.New(slog.NewTextHandler(io.Discard, nil))).Send(t.Context())
	if err == nil {
		t.Fatal("a refused heartbeat reported success")
	}
}
