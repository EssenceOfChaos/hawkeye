package worker_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidents"
	"hawkeye/internal/regions"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/worker"
)

const recheckDelay = 50 * time.Millisecond

func (e *env) recheckWorker(set regions.Set, region string) *worker.Worker {
	store := checks.NewStore(e.pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{}).WithRecheck(recheckDelay)
	w := worker.New(e.pool, checks.NewChecker(loopbackCfg()), store.WithRegions(set), worker.Config{Concurrency: 1, PollInterval: 10 * time.Millisecond}, e.m, quiet)
	return w.WithRegion(region)
}

// drain runs the workers until the monitor has no job left, waiting out re-check delays.
func (e *env) drain(t *testing.T, id uuid.UUID, ws ...*worker.Worker) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, w := range ws {
			if _, err := w.ProcessOne(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		var left int
		e.pool.QueryRow(t.Context(), `SELECT count(*) FROM check_jobs WHERE monitor_id = $1`, id).Scan(&left)
		if left == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("jobs still queued")
}

func (e *env) tick(t *testing.T, s *scheduler.Scheduler, id uuid.UUID) {
	t.Helper()
	e.pool.Exec(t.Context(), `UPDATE monitors SET next_check_at = now() WHERE id = $1`, id)
	if _, err := s.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// resultFlags lists the monitor's results, oldest first, as "ok"/"fail" with "+re" for re-checks.
func (e *env) resultFlags(t *testing.T, id uuid.UUID) string {
	t.Helper()
	rows, err := e.pool.Query(t.Context(), `SELECT success, recheck FROM check_results WHERE monitor_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ok, re bool
		rows.Scan(&ok, &re)
		s := map[bool]string{true: "ok", false: "fail"}[ok]
		if re {
			s += "+re"
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

// failing serves 503 for the first n requests (n < 0: always), then 200.
func failing(n int32) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if h := hits.Add(1); n < 0 || h <= n {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	})), &hits
}

func TestARealOutageIsConfirmedByAnImmediateRecheck(t *testing.T) {
	e := newEnv(t)
	srv, hits := failing(-1)
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	w := e.recheckWorker(regions.Default(), "primary")

	e.tick(t, e.sched, id)
	e.drain(t, id, w)
	if s := e.state(t, id); s.Status != checks.StatusDown || s.Failures != 2 {
		t.Fatalf("state %+v: the re-check must confirm the outage without waiting an interval", s)
	}
	if got := e.resultFlags(t, id); got != "fail,fail+re" || hits.Load() != 2 {
		t.Fatalf("results %s, %d requests", got, hits.Load())
	}
	var summary string
	e.pool.QueryRow(t.Context(), `SELECT summary FROM incidents WHERE monitor_id = $1`, id).Scan(&summary)
	if !strings.Contains(summary, "confirmed by an immediate re-check") {
		t.Fatalf("incident summary %q", summary)
	}
	if got := testutil.ToFloat64(e.m.Rechecks.WithLabelValues("failed")); got != 1 {
		t.Fatalf("failed re-checks = %v", got)
	}

	// While down, failures are not re-checked: the site is checked at its normal rate.
	e.tick(t, e.sched, id)
	e.drain(t, id, w)
	if got := e.resultFlags(t, id); got != "fail,fail+re,fail" {
		t.Fatalf("results %s", got)
	}
}

func TestABlipIsClearedByAnImmediateRecheck(t *testing.T) {
	e := newEnv(t)
	srv, _ := failing(1)
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	w := e.recheckWorker(regions.Default(), "primary")

	e.tick(t, e.sched, id)
	e.drain(t, id, w)
	if s := e.state(t, id); s.Status != checks.StatusUp || s.Failures != 0 {
		t.Fatalf("state %+v", s)
	}
	if got := e.resultFlags(t, id); got != "fail,ok+re" {
		t.Fatalf("results %s", got)
	}
	var n int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM incidents WHERE monitor_id = $1`, id).Scan(&n)
	if n != 0 {
		t.Fatal("a blip cleared by the re-check must not open an incident")
	}
	if got := testutil.ToFloat64(e.m.Rechecks.WithLabelValues("passed")); got != 1 {
		t.Fatalf("passed re-checks = %v", got)
	}
}

// A request that may change something is not sent again unless the customer says it is safe to repeat.
func TestWriteRequestsAreRecheckedOnlyWhenAllowed(t *testing.T) {
	for _, c := range []struct {
		method, setting, want string
	}{
		{"POST", "auto", "fail"},
		{"POST", "always", "fail,fail+re"},
		{"GET", "off", "fail"},
		{"HEAD", "auto", "fail,fail+re"},
	} {
		t.Run(c.method+" "+c.setting, func(t *testing.T) {
			e := newEnv(t)
			srv, _ := failing(-1)
			defer srv.Close()
			id := e.monitor(t, srv.URL)
			if _, err := e.pool.Exec(t.Context(), `UPDATE monitors SET method = $2, recheck = $3, check_interval_seconds = 300 WHERE id = $1`,
				id, c.method, c.setting); err != nil {
				t.Fatal(err)
			}
			e.tick(t, e.sched, id)
			e.drain(t, id, e.recheckWorker(regions.Default(), "primary"))
			if got := e.resultFlags(t, id); got != c.want {
				t.Fatalf("results %s, want %s", got, c.want)
			}
		})
	}
}

// Multi-step checks: one write step is enough to keep "auto" from re-checking.
func TestWorkflowsWithAWriteStepAreNotRecheckedByDefault(t *testing.T) {
	e := newEnv(t)
	store := checks.NewStore(e.pool, checks.Thresholds{Failure: 2, Recovery: 2}, nil).WithRecheck(recheckDelay)
	for _, c := range []struct {
		methods []string
		want    bool
	}{
		{[]string{"GET", "GET"}, true},
		{[]string{"POST", "GET"}, false},
	} {
		var id uuid.UUID
		if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, kind, check_interval_seconds, next_check_at)
			VALUES ($1, 'w', 'https://example.com', 'workflow', 300, now() + interval '5 minutes') RETURNING id`, e.org).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for i, m := range c.methods {
			if _, err := e.pool.Exec(t.Context(), `INSERT INTO monitor_steps (monitor_id, position, name, method, url) VALUES ($1, $2, 's', $3, 'https://example.com')`,
				id, i, m); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now()
		out, err := store.Record(t.Context(), id, checks.Result{CheckedAt: now, FinishedAt: now, ErrorCategory: checks.CatHTTPStatus})
		if err != nil {
			t.Fatal(err)
		}
		if out.Recheck != c.want {
			t.Fatalf("steps %v: Recheck = %v", c.methods, out.Recheck)
		}
	}
}

// With several locations, each failing location re-checks itself and the majority forms right away; one location alone still alerts nobody.
func TestMultiLocationOutagesAreConfirmedByEachLocationsRecheck(t *testing.T) {
	set, _ := regions.Parse("us,eu,ap")
	for _, c := range []struct {
		name         string
		failIn       map[string]bool
		wantStatus   string
		wantRechecks int
	}{
		// us and eu each confirm their failure, which makes the majority; ap then finds the monitor already down, so it isn't re-checked.
		{"down everywhere", map[string]bool{"us": true, "eu": true, "ap": true}, checks.StatusDown, 2},
		{"down in one location", map[string]bool{"eu": true}, checks.StatusUp, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.failIn[r.URL.Query().Get("from")] {
					w.WriteHeader(503)
					return
				}
				w.WriteHeader(200)
			}))
			defer srv.Close()
			id := e.monitor(t, srv.URL)
			e.pool.Exec(t.Context(), `UPDATE monitors SET regions = '{us,eu,ap}' WHERE id = $1`, id)
			sched := scheduler.New(e.pool, scheduler.Config{}, e.m, quiet).WithRegions(set)
			var ws []*worker.Worker
			for _, r := range []string{"us", "eu", "ap"} {
				ws = append(ws, e.recheckWorker(set, r))
			}
			// The fake site can't tell locations apart, so each location runs in turn with its name in the address.
			e.tick(t, sched, id)
			for i, r := range []string{"us", "eu", "ap"} {
				e.pool.Exec(t.Context(), `UPDATE monitors SET url = $2 WHERE id = $1`, id, srv.URL+"/?from="+r)
				e.drainRegion(t, id, r, ws[i])
			}
			if s := e.state(t, id); s.Status != c.wantStatus {
				t.Fatalf("status %s, want %s; results %s", s.Status, c.wantStatus, e.resultFlags(t, id))
			}
			var rechecks int
			e.pool.QueryRow(t.Context(), `SELECT count(*) FROM check_results WHERE monitor_id = $1 AND recheck`, id).Scan(&rechecks)
			if rechecks != c.wantRechecks {
				t.Fatalf("%d re-checks, want %d", rechecks, c.wantRechecks)
			}
		})
	}
}

// drainRegion runs one location's worker until that location's job is done, re-check included.
func (e *env) drainRegion(t *testing.T, id uuid.UUID, region string, w *worker.Worker) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := w.ProcessOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		var left int
		e.pool.QueryRow(t.Context(), `SELECT count(*) FROM check_jobs WHERE monitor_id = $1 AND region = $2`, id, region).Scan(&left)
		if left == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the %s job is still queued", region)
}

// Regression: a location whose very first check failed used to fail recording (an empty status rejected by monitor_region_state), so the result
// was lost and retried until dropped.
func TestAFirstFailureInALocationIsRecorded(t *testing.T) {
	e := newEnv(t)
	set, _ := regions.Parse("us,eu")
	srv, _ := failing(-1)
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	e.pool.Exec(t.Context(), `UPDATE monitors SET regions = '{us,eu}' WHERE id = $1`, id)
	store := checks.NewStore(e.pool, checks.Thresholds{Failure: 2, Recovery: 2}, nil).WithRegions(set)
	now := time.Now()
	if _, err := store.Record(t.Context(), id, checks.Result{CheckedAt: now, FinishedAt: now, Region: "us", ErrorCategory: checks.CatHTTPStatus}); err != nil {
		t.Fatal(err)
	}
	var status string
	var failures int
	e.pool.QueryRow(t.Context(), `SELECT status, consecutive_failures FROM monitor_region_state WHERE monitor_id = $1 AND region = 'us'`, id).Scan(&status, &failures)
	if status != checks.StatusUnknown || failures != 1 {
		t.Fatalf("us: %s with %d failures", status, failures)
	}
}
