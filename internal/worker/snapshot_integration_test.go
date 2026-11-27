package worker_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidents"
	"hawkeye/internal/regions"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/testutil"
	"hawkeye/internal/worker"
)

// snapshotWorker is a worker whose store opens incidents (with the secrets box, so excerpts can be sealed).
func (e *env) snapshotWorker(cfg safehttp.Config) *worker.Worker {
	box := testutil.SecretBox()
	return worker.New(e.pool, checks.NewChecker(cfg), checks.NewStore(e.pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{Box: box}),
		worker.Config{Concurrency: 5, PollInterval: 20 * time.Millisecond}, e.m, quiet).WithSecrets(box)
}

// flakySite answers 200 with JSON until broken is set, then 502 with an HTML page that echoes the monitor's API key.
func flakySite(broken *atomic.Bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !broken.Load() {
			w.Header().Set("Server", "nginx")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","items":[` + strings.Repeat(`"x",`, 2000) + `"x"]}`))
			return
		}
		w.Header().Set("Server", "cloudflare")
		w.Header().Set("Set-Cookie", "session=s3cr3t")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<h1>Bad gateway</h1> key " + r.Header.Get("X-Api-Key")))
	}))
}

func (e *env) openIncident(t *testing.T, monitor uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `SELECT id FROM incidents WHERE monitor_id = $1 AND status = 'open'`, monitor).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAnOutageKeepsWhatHawkEyeSawAndWhatChanged(t *testing.T) {
	e := newEnv(t)
	var broken atomic.Bool
	srv := flakySite(&broken)
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	w := e.snapshotWorker(loopbackCfg())

	// Good checks keep the last good response, written once while it stays the same.
	e.round(t, w)
	var fp1, fp2 string
	e.pool.QueryRow(t.Context(), `SELECT COALESCE((SELECT fingerprint FROM monitor_last_good WHERE monitor_id = $1), '')`, id).Scan(&fp1)
	e.round(t, w)
	e.pool.QueryRow(t.Context(), `SELECT COALESCE((SELECT fingerprint FROM monitor_last_good WHERE monitor_id = $1), '')`, id).Scan(&fp2)
	if fp1 == "" || fp1 != fp2 {
		t.Fatalf("last good fingerprint %q then %q", fp1, fp2)
	}

	broken.Store(true)
	e.round(t, w)
	e.round(t, w) // the second failure confirms the outage
	inc := e.openIncident(t, id)
	s, err := incidents.LoadSnapshot(t.Context(), e.pool, inc)
	if err != nil || s == nil {
		t.Fatalf("%v %+v", err, s)
	}
	if s.Facts.StatusCode != 502 || s.Facts.ContentType != "text/html" || s.HasExcerpt || s.StepIndex != nil {
		t.Fatalf("snapshot %+v", s)
	}
	for _, h := range s.Facts.Headers {
		if h.Name == "Set-Cookie" {
			t.Fatal("Set-Cookie kept")
		}
	}
	var lines []string
	for _, c := range s.Changes {
		lines = append(lines, c.String())
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"Status: 200 → 502", "Content type: application/json → text/html", "Server: nginx → cloudflare", "Size: 8.0 KB → "} {
		if !strings.Contains(got, want) {
			t.Errorf("changes lack %q:\n%s", want, got)
		}
	}
}

func TestTheBodyExcerptIsOptInSealedAndHidesTheKey(t *testing.T) {
	e := newEnv(t)
	var broken atomic.Bool
	broken.Store(true)
	srv := flakySite(&broken)
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	box := testutil.SecretBox()
	sealed, err := box.Seal("key-0123456789", id.String())
	if err != nil {
		t.Fatal(err)
	}
	e.pool.Exec(t.Context(), `UPDATE monitors SET auth_type = 'header', auth_header_name = 'X-Api-Key', auth_secret = $2, capture_failure_body = true
		WHERE id = $1`, id, sealed)
	w := e.snapshotWorker(loopbackCfg())
	e.round(t, w)
	e.round(t, w)
	inc := e.openIncident(t, id)

	var stored string
	e.pool.QueryRow(t.Context(), `SELECT body_excerpt FROM incident_snapshots WHERE incident_id = $1`, inc).Scan(&stored)
	if strings.Contains(stored, "Bad gateway") {
		t.Fatal("the excerpt is stored in the clear")
	}
	ex, err := incidents.LoadExcerpt(t.Context(), e.pool, box, inc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ex.Text, "<h1>Bad gateway</h1>") || strings.Contains(ex.Text, "key-0123456789") || !strings.Contains(ex.Text, "[hidden]") {
		t.Fatalf("excerpt %q", ex.Text)
	}
	// Sealed to its incident: another incident's context can't open it.
	if _, err := box.Open(stored, incidents.ExcerptContext(uuid.New())); err == nil {
		t.Fatal("the excerpt opened under another incident")
	}
}

func TestNoExcerptUnlessTheMonitorOptedIn(t *testing.T) {
	e := newEnv(t)
	var broken atomic.Bool
	broken.Store(true)
	srv := flakySite(&broken)
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	w := e.snapshotWorker(loopbackCfg())
	e.round(t, w)
	e.round(t, w)
	inc := e.openIncident(t, id)
	if _, err := incidents.LoadExcerpt(t.Context(), e.pool, testutil.SecretBox(), inc); err != incidents.ErrNoExcerpt {
		t.Fatalf("err %v", err)
	}
	// A new monitor has no last good response: a snapshot, but no changes.
	s, err := incidents.LoadSnapshot(t.Context(), e.pool, inc)
	if err != nil || s == nil || len(s.Changes) != 0 {
		t.Fatalf("%v %+v", err, s)
	}
}

// Each location keeps its own last good response, and a failure is compared with what the same location saw: geo-DNS and CDNs answer each
// location from other servers, which must not show up as changes.
func TestEachLocationIsComparedWithItsOwnLastGoodResponse(t *testing.T) {
	e := newEnv(t)
	set, _ := regions.Parse("us,eu")
	id := e.monitor(t, "https://example.com")
	e.pool.Exec(t.Context(), `UPDATE monitors SET regions = '{us,eu}' WHERE id = $1`, id)
	store := checks.NewStore(e.pool, checks.Thresholds{Failure: 1, Recovery: 1}, incidents.Engine{Box: testutil.SecretBox()}).WithRegions(set)
	record := func(region string, ok bool, addr, server string) {
		t.Helper()
		now := time.Now()
		code := 200
		r := checks.Result{CheckedAt: now, FinishedAt: now, Region: region, Success: ok, StatusCode: &code, FailedStep: -1,
			Facts: &checks.ResponseFacts{StatusCode: code, SizeBytes: 100, ServerAddr: addr, Headers: []checks.Header{{Name: "Server", Value: server}}}}
		if !ok {
			code = 503
			r.ErrorCategory, r.ErrorSummary, r.Facts.StatusCode = checks.CatHTTPStatus, "Received HTTP 503", 503
		}
		if _, err := store.Record(t.Context(), id, r); err != nil {
			t.Fatal(err)
		}
	}
	record("us", true, "198.51.100.7", "edge-us")
	record("eu", true, "203.0.113.7", "edge-eu")
	var n int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM monitor_last_good WHERE monitor_id = $1`, id).Scan(&n)
	if n != 2 {
		t.Fatalf("%d last good responses, want one per location", n)
	}
	// Both fail from their own usual servers: the only change is the status, not a server or address from the other location.
	record("us", false, "198.51.100.7", "edge-us")
	record("eu", false, "203.0.113.7", "edge-eu")
	s, err := incidents.LoadSnapshot(t.Context(), e.pool, e.openIncident(t, id))
	if err != nil || s == nil {
		t.Fatalf("%v %+v", err, s)
	}
	if len(s.Changes) != 1 || s.Changes[0].Field != "status" {
		t.Fatalf("changes %+v", s.Changes)
	}
}

// With capture on, a response with no body says so instead of asking to turn on the setting that already is.
func TestABodylessFailureIsRecordedAsRequested(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	e.pool.Exec(t.Context(), `UPDATE monitors SET capture_failure_body = true WHERE id = $1`, id)
	w := e.snapshotWorker(loopbackCfg())
	e.round(t, w)
	e.round(t, w)
	s, err := incidents.LoadSnapshot(t.Context(), e.pool, e.openIncident(t, id))
	if err != nil || s == nil || s.HasExcerpt || !s.BodyRequested || s.BodyBinary {
		t.Fatalf("%v %+v", err, s)
	}
}
