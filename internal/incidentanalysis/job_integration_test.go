package incidentanalysis_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidentanalysis"
	"hawkeye/internal/incidents"
	"hawkeye/internal/metrics"
	"hawkeye/internal/testutil"
)

// fake answers with result, or err, and remembers what it was sent.
type fake struct {
	mu    sync.Mutex
	err   error
	calls []incidentanalysis.Facts
}

func (f *fake) Analyze(_ context.Context, facts incidentanalysis.Facts) (incidentanalysis.Result, incidentanalysis.Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, facts)
	if f.err != nil {
		return incidentanalysis.Result{}, incidentanalysis.Usage{}, f.err
	}
	return incidentanalysis.Result{Category: "origin_server", LikelyCause: "The origin server is failing behind the proxy.",
		Evidence: []string{"503 from every check"}, NextStep: "Check the origin's logs.", Confidence: "medium"}, incidentanalysis.Usage{InputTokens: 1500, OutputTokens: 200}, nil
}

type env struct {
	pool  *pgxpool.Pool
	store *checks.Store
	org   uuid.UUID
	ai    *fake
	job   *incidentanalysis.Job
	clock time.Time
}

func newEnv(t *testing.T, plan string, enabled bool) *env {
	t.Helper()
	pool := testutil.NewPool(t)
	e := &env{pool: pool, store: checks.NewStore(pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{}), ai: &fake{},
		clock: time.Now().UTC().Add(-time.Hour).Truncate(time.Second)}
	e.job = incidentanalysis.NewJob(pool, e.ai, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name, ai_analysis_enabled) VALUES ('o', $1) RETURNING id`, enabled).Scan(&e.org); err != nil {
		t.Fatal(err)
	}
	if plan != "free" {
		if _, err := pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = $2`, e.org, plan); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// outage opens an incident on a new monitor, with a snapshot (the failing checks got a response), and returns its ID.
func (e *env) outage(t *testing.T) uuid.UUID {
	t.Helper()
	var mon uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, expected_status_code)
		VALUES ($1, 'Shop', 'https://secret-shop.example.com/', 60, 200) RETURNING id`, e.org).Scan(&mon); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		e.clock = e.clock.Add(time.Minute)
		code := 503
		r := checks.Result{CheckedAt: e.clock, FinishedAt: e.clock.Add(200 * time.Millisecond), StatusCode: &code,
			ErrorCategory: checks.CatHTTPStatus, ErrorSummary: "Received HTTP 503",
			Facts: &checks.ResponseFacts{StatusCode: 503, FinalURL: "https://secret-shop.example.com/", SizeBytes: 20, ServerAddr: "198.51.100.9",
				Headers: []checks.Header{{Name: "Server", Value: "cloudflare"}}}}
		if _, err := e.store.Record(t.Context(), mon, r); err != nil {
			t.Fatal(err)
		}
	}
	var inc uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `SELECT id FROM incidents WHERE monitor_id = $1`, mon).Scan(&inc); err != nil {
		t.Fatal(err)
	}
	return inc
}

func (e *env) status(t *testing.T, inc uuid.UUID) string {
	t.Helper()
	var s string
	if err := e.pool.QueryRow(t.Context(), `SELECT COALESCE((SELECT status FROM incident_analyses WHERE incident_id = $1), 'none')`, inc).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) used(t *testing.T) int {
	t.Helper()
	n, err := incidentanalysis.UsedThisMonth(t.Context(), e.pool, e.org)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) runAll(t *testing.T) {
	t.Helper()
	for range 10 {
		worked, err := e.job.RunOne(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			return
		}
	}
}

func TestAConfirmedOutageIsAnalyzedOnceForAnOrganizationThatTurnedItOn(t *testing.T) {
	e := newEnv(t, "pro", true)
	inc := e.outage(t)
	if s := e.status(t, inc); s != "pending" {
		t.Fatalf("queued with the incident: %s", s)
	}
	e.runAll(t)
	a, err := incidentanalysis.Load(t.Context(), e.pool, inc)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "done" || *a.Category != "origin_server" || *a.Confidence != "medium" || len(a.Evidence) != 1 || *a.Model != incidentanalysis.Model {
		t.Fatalf("%+v", a)
	}
	if e.used(t) != 1 || len(e.ai.calls) != 1 {
		t.Fatalf("used %d, calls %d", e.used(t), len(e.ai.calls))
	}
	f := e.ai.calls[0]
	if f.Response.StatusCode != 503 || f.Category != checks.CatHTTPStatus || f.ExpectedStatus == nil || *f.ExpectedStatus != 200 {
		t.Fatalf("facts: %+v", f)
	}

	// Asking again replaces it and counts again.
	if err := incidentanalysis.Request(t.Context(), e.pool, e.org, inc); err != nil {
		t.Fatal(err)
	}
	if err := incidentanalysis.Request(t.Context(), e.pool, e.org, inc); err != incidentanalysis.ErrInProgress {
		t.Fatalf("second request: %v", err)
	}
	e.runAll(t)
	if e.status(t, inc) != "done" || e.used(t) != 2 {
		t.Fatalf("re-run: %s, used %d", e.status(t, inc), e.used(t))
	}
}

func TestNothingIsQueuedOrSentWhenAnalysisIsOff(t *testing.T) {
	e := newEnv(t, "pro", false)
	inc := e.outage(t)
	e.runAll(t)
	if s := e.status(t, inc); s != "none" || len(e.ai.calls) != 0 {
		t.Fatalf("status %s, calls %d", s, len(e.ai.calls))
	}

	// Turned off after the outage was queued: the run ends without calling the model.
	e2 := newEnv(t, "pro", true)
	inc2 := e2.outage(t)
	if _, err := e2.pool.Exec(t.Context(), `UPDATE organizations SET ai_analysis_enabled = false WHERE id = $1`, e2.org); err != nil {
		t.Fatal(err)
	}
	e2.runAll(t)
	if s := e2.status(t, inc2); s != "off" || len(e2.ai.calls) != 0 || e2.used(t) != 0 {
		t.Fatalf("status %s, calls %d, used %d", s, len(e2.ai.calls), e2.used(t))
	}
}

func TestTheMonthlyAllowanceIsEnforcedWhenTheRunStarts(t *testing.T) {
	e := newEnv(t, "free", true)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO ai_analysis_usage (organization_id, incident_id, requested_at)
		SELECT $1, gen_random_uuid(), now() - interval '2 hours' FROM generate_series(1, 25)`, e.org); err != nil {
		t.Fatal(err)
	}
	inc := e.outage(t)
	e.runAll(t)
	if s := e.status(t, inc); s != "over_limit" || len(e.ai.calls) != 0 || e.used(t) != 25 {
		t.Fatalf("status %s, calls %d, used %d", s, len(e.ai.calls), e.used(t))
	}
}

func TestDeclinedAndFailedRunsDoNotCount(t *testing.T) {
	e := newEnv(t, "pro", true)
	e.ai.err = incidentanalysis.ErrRefused
	inc := e.outage(t)
	e.runAll(t)
	if s := e.status(t, inc); s != "declined" || e.used(t) != 0 {
		t.Fatalf("declined: %s, used %d", s, e.used(t))
	}

	e.ai.err = incidentanalysis.ErrUnavailable
	if err := incidentanalysis.Request(t.Context(), e.pool, e.org, inc); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= incidentanalysis.MaxAttempts; attempt++ {
		e.runAll(t)
		// Retries wait a while; bring the next one forward.
		if _, err := e.pool.Exec(t.Context(), `UPDATE incident_analyses SET run_at = now() WHERE incident_id = $1`, inc); err != nil {
			t.Fatal(err)
		}
	}
	if s := e.status(t, inc); s != "failed" || e.used(t) != 0 || len(e.ai.calls) != 1+incidentanalysis.MaxAttempts {
		t.Fatalf("failed: %s, used %d, calls %d", s, e.used(t), len(e.ai.calls))
	}
}

func TestTheHourlyLimitDelaysRunsInsteadOfDroppingThem(t *testing.T) {
	e := newEnv(t, "agency", true)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO ai_analysis_usage (organization_id, incident_id, requested_at)
		SELECT $1, gen_random_uuid(), now() FROM generate_series(1, $2::int)`, e.org, incidentanalysis.HourlyLimit); err != nil {
		t.Fatal(err)
	}
	inc := e.outage(t)
	e.runAll(t)
	var later bool
	if err := e.pool.QueryRow(t.Context(), `SELECT run_at > now() + interval '4 minutes' FROM incident_analyses WHERE incident_id = $1`, inc).Scan(&later); err != nil {
		t.Fatal(err)
	}
	if s := e.status(t, inc); s != "pending" || !later || len(e.ai.calls) != 0 {
		t.Fatalf("status %s, later %v, calls %d", s, later, len(e.ai.calls))
	}
}

func TestRunsRecordHowLongTheModelAndTheWholeAnalysisTook(t *testing.T) {
	e := newEnv(t, "pro", true)
	m := metrics.New()
	e.job = incidentanalysis.NewJob(e.pool, e.ai, m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.outage(t)
	e.runAll(t)
	if n := promtest.CollectAndCount(m.AIAnalysisModel, "hawkeye_ai_analysis_model_seconds"); n != 1 {
		t.Fatalf("model series: %d", n)
	}
	if got := histogramCount(t, m.AIAnalysisLatency); got != 1 {
		t.Fatalf("latency observations: %d", got)
	}

	// A run the model declined is timed too, but has no analysis to measure the latency of.
	e.ai.err = incidentanalysis.ErrRefused
	e.outage(t)
	e.runAll(t)
	if n := promtest.CollectAndCount(m.AIAnalysisModel, "hawkeye_ai_analysis_model_seconds"); n != 2 {
		t.Fatalf("model series after a decline: %d", n)
	}
	if got := histogramCount(t, m.AIAnalysisLatency); got != 1 {
		t.Fatalf("latency observations after a decline: %d", got)
	}
}

func histogramCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()
	var out dto.Metric
	if err := h.Write(&out); err != nil {
		t.Fatal(err)
	}
	return out.GetHistogram().GetSampleCount()
}
