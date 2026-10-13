package worker_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/email"
	"hawkeye/internal/incidents"
	"hawkeye/internal/metrics"
	"hawkeye/internal/notifications"
	"hawkeye/internal/queue"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/testutil"
	"hawkeye/internal/worker"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// loopbackCfg reaches httptest servers while all other destinations stay prohibited.
func loopbackCfg() safehttp.Config {
	return safehttp.Config{
		AllowIP:   func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") || safehttp.IsPublicIP(a) },
		AllowPort: func(int) bool { return true },
	}
}

type env struct {
	pool  *pgxpool.Pool
	sched *scheduler.Scheduler
	m     *metrics.Metrics
	org   uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewPoolSize(t, 20)
	var org uuid.UUID
	pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org)
	pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code='pro'`, org)
	m := metrics.New()
	return &env{pool: pool, m: m, org: org, sched: scheduler.New(pool, scheduler.Config{}, m, quiet)}
}

func (e *env) worker(cfg safehttp.Config) *worker.Worker {
	return worker.New(e.pool, checks.NewChecker(cfg), checks.NewStore(e.pool, checks.Thresholds{Failure: 2, Recovery: 2}, nil),
		worker.Config{Concurrency: 5, PollInterval: 20 * time.Millisecond}, e.m, quiet)
}

func (e *env) monitor(t *testing.T, url string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, timeout_seconds)
		VALUES ($1, 'm', $2, 60, 2) RETURNING id`, e.org, url).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// round schedules everything due and processes jobs until the queue is empty.
func (e *env) round(t *testing.T, w *worker.Worker) {
	t.Helper()
	e.pool.Exec(t.Context(), `UPDATE monitors SET next_check_at = now() WHERE enabled AND deleted_at IS NULL`)
	if _, err := e.sched.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	for {
		worked, err := w.ProcessOne(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			return
		}
	}
}

type monState struct {
	Status              string
	Failures, Successes int
	LastChecked         *time.Time
	LastSuccess         *time.Time
}

func (e *env) state(t *testing.T, id uuid.UUID) monState {
	t.Helper()
	var s monState
	if err := e.pool.QueryRow(t.Context(), `SELECT current_status, consecutive_failures, consecutive_successes, last_checked_at, last_success_at
		FROM monitors WHERE id = $1`, id).Scan(&s.Status, &s.Failures, &s.Successes, &s.LastChecked, &s.LastSuccess); err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) results(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM check_results WHERE monitor_id = $1`, id).Scan(&n)
	return n
}

func TestHealthySiteIsCheckedAndRecorded(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	e.round(t, e.worker(loopbackCfg()))

	s := e.state(t, id)
	if s.Status != "up" || s.Successes != 1 || s.LastChecked == nil || s.LastSuccess == nil || e.results(t, id) != 1 {
		t.Fatalf("%+v results=%d", s, e.results(t, id))
	}
	var code, ms int
	var cat *string
	e.pool.QueryRow(t.Context(), `SELECT http_status_code, response_time_ms, error_category FROM check_results WHERE monitor_id=$1`, id).Scan(&code, &ms, &cat)
	if code != 200 || cat != nil {
		t.Fatalf("code=%d cat=%v", code, cat)
	}
	var jobs int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM check_jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("completed jobs must be removed")
	}
}

func TestOutageNeedsTwoFailuresAndRecoveryNeedsTwoSuccesses(t *testing.T) {
	e := newEnv(t)
	var status atomic.Int32
	status.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	w := e.worker(loopbackCfg())

	e.round(t, w)
	status.Store(503)
	e.round(t, w)
	if s := e.state(t, id); s.Status != "up" || s.Failures != 1 {
		t.Fatalf("one failure must not flip the status: %+v", s)
	}
	e.round(t, w)
	if s := e.state(t, id); s.Status != "down" || s.Failures != 2 {
		t.Fatalf("two failures confirm the outage: %+v", s)
	}
	status.Store(200)
	e.round(t, w)
	if s := e.state(t, id); s.Status != "down" || s.Successes != 1 {
		t.Fatalf("one success must not clear an outage: %+v", s)
	}
	e.round(t, w)
	if s := e.state(t, id); s.Status != "up" {
		t.Fatalf("two successes recover: %+v", s)
	}
	if e.results(t, id) != 5 {
		t.Fatalf("results = %d", e.results(t, id))
	}
}

func TestProhibitedDestinationsAreRecordedAsBlockedFailures(t *testing.T) {
	e := newEnv(t)
	// These bypass the API validation on purpose: the worker must hold the line on its own.
	ids := []uuid.UUID{e.monitor(t, "http://169.254.169.254/latest/meta-data/"), e.monitor(t, "http://10.0.0.1/"), e.monitor(t, "http://[::1]/")}
	e.round(t, e.worker(safehttp.Config{})) // production policy
	for _, id := range ids {
		var cat, summary string
		var ok bool
		e.pool.QueryRow(t.Context(), `SELECT success, error_category, error_summary FROM check_results WHERE monitor_id=$1`, id).Scan(&ok, &cat, &summary)
		if ok || cat != "blocked" || summary != "Destination is not permitted" {
			t.Errorf("%s: ok=%v cat=%q summary=%q", id, ok, cat, summary)
		}
	}
}

func TestPausedOrDeletedMonitorResultIsDiscarded(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	paused, deleted := e.monitor(t, srv.URL), e.monitor(t, srv.URL)
	e.sched.Tick(t.Context())
	e.pool.Exec(t.Context(), `UPDATE monitors SET enabled = false WHERE id = $1`, paused)
	e.pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() WHERE id = $1`, deleted)

	w := e.worker(loopbackCfg())
	for range 3 {
		w.ProcessOne(t.Context())
	}
	if e.results(t, paused) != 0 || e.results(t, deleted) != 0 {
		t.Fatal("results for inactive monitors must not be recorded")
	}
	var jobs int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM check_jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Fatalf("%d jobs left behind", jobs)
	}
}

// Phase 4 acceptance: restarting a worker does not permanently lose scheduled work.
func TestJobSurvivesWorkerCrash(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	e.sched.Tick(t.Context())

	// First worker claims the job and dies without finishing or releasing it.
	crashed, _, err := queue.Claim(t.Context(), e.pool, time.Minute, 3)
	if err != nil || crashed == nil {
		t.Fatalf("%v %v", crashed, err)
	}
	if worked, _ := e.worker(loopbackCfg()).ProcessOne(t.Context()); worked {
		t.Fatal("a live lease must not be stolen")
	}

	// Lease expires (time passes); a freshly started worker picks the job up.
	e.pool.Exec(t.Context(), `UPDATE check_jobs SET locked_until = now() - interval '1 second'`)
	if worked, err := e.worker(loopbackCfg()).ProcessOne(t.Context()); !worked || err != nil {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if e.results(t, id) != 1 || e.state(t, id).Status != "up" {
		t.Fatalf("work was lost: results=%d %+v", e.results(t, id), e.state(t, id))
	}
}

func TestRunProcessesAllJobsExactlyOnceAndStopsOnCancel(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	const n = 25
	var ids []uuid.UUID
	for range n {
		ids = append(ids, e.monitor(t, srv.URL))
	}
	e.sched.Tick(t.Context())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.worker(loopbackCfg()).Run(ctx); close(done) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var jobs int
		e.pool.QueryRow(t.Context(), `SELECT count(*) FROM check_jobs`).Scan(&jobs)
		if jobs == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
	for _, id := range ids {
		if got := e.results(t, id); got != 1 {
			t.Fatalf("monitor %s has %d results, want exactly 1", id, got)
		}
	}
	if hits.Load() != n {
		t.Fatalf("target was hit %d times, want %d", hits.Load(), n)
	}
}

func TestSampleExposesQueueGauges(t *testing.T) {
	e := newEnv(t)
	e.monitor(t, "https://example.com")
	e.sched.Tick(t.Context())
	if err := worker.Sample(t.Context(), e.pool, e.m); err != nil {
		t.Fatal(err)
	}
	families, _ := e.m.Registry.Gather()
	found := map[string]bool{}
	for _, f := range families {
		found[f.GetName()] = true
	}
	for _, name := range []string{"hawkeye_queue_depth", "hawkeye_scheduler_lag_seconds", "hawkeye_scheduler_jobs_scheduled_total"} {
		if !found[name] {
			t.Errorf("metric %s not exported", name)
		}
	}
}

// End to end: a real HTTP outage and recovery produce incidents and the right two emails.
func TestOutageAndRecoveryEndToEnd(t *testing.T) {
	e := newEnv(t)
	var status atomic.Int32
	status.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	e.pool.Exec(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination, verified_at) VALUES ($1, 'email', 'owner@example.com', now())`, e.org)

	w := worker.New(e.pool, checks.NewChecker(loopbackCfg()),
		checks.NewStore(e.pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{}),
		worker.Config{Concurrency: 1, PollInterval: 10 * time.Millisecond}, e.m, quiet)
	mail := &email.Memory{}
	disp := notifications.NewDispatcher(e.pool, mail, notifications.DispatcherConfig{AppBaseURL: "https://app.example.com"}, e.m, quiet)
	drain := func() {
		for {
			if worked, err := disp.ProcessOne(t.Context()); err != nil || !worked {
				return
			}
		}
	}

	e.round(t, w) // healthy
	status.Store(500)
	e.round(t, w)
	drain()
	if len(mail.Messages()) != 0 {
		t.Fatal("one failed check must not alert anyone")
	}
	e.round(t, w) // second failure confirms the outage
	drain()
	msgs := mail.Messages()
	if len(msgs) != 1 || msgs[0].To != "owner@example.com" || !strings.Contains(msgs[0].Subject, "may be down") || !strings.Contains(msgs[0].Text, "Received HTTP 500") {
		t.Fatalf("outage mail: %+v", msgs)
	}
	e.round(t, w)
	drain()
	if len(mail.Messages()) != 1 {
		t.Fatal("an ongoing outage must not send more mail")
	}

	status.Store(200)
	e.round(t, w)
	e.round(t, w) // second success confirms recovery
	drain()
	msgs = mail.Messages()
	if len(msgs) != 2 || !strings.Contains(msgs[1].Subject, "is responding again") {
		t.Fatalf("recovery mail: %+v", msgs)
	}
	var open, resolved int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE status='open'), count(*) FILTER (WHERE status='resolved') FROM incidents WHERE monitor_id=$1`, id).Scan(&open, &resolved)
	if open != 0 || resolved != 1 {
		t.Fatalf("open=%d resolved=%d", open, resolved)
	}
}
