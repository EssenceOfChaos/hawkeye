package scheduler_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/metrics"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/testutil"
)

func newScheduler(pool *pgxpool.Pool, cfg scheduler.Config) *scheduler.Scheduler {
	return scheduler.New(pool, cfg, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newOrg(t *testing.T, pool *pgxpool.Pool, plan, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status)
		SELECT $1, id, $3 FROM plans WHERE code = $2`, id, plan, status); err != nil {
		t.Fatal(err)
	}
	return id
}

func newMonitor(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, interval int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds)
		VALUES ($1, 'm', 'https://example.com', $2) RETURNING id`, org, interval).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func jobCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM check_jobs`).Scan(&n)
	return n
}

// secondsUntilNext returns next_check_at - now() in seconds.
func secondsUntilNext(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) float64 {
	t.Helper()
	var s float64
	if err := pool.QueryRow(t.Context(), `SELECT EXTRACT(EPOCH FROM next_check_at - now())::float8 FROM monitors WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDueMonitorGetsOneJobAndIsRescheduled(t *testing.T) {
	pool := testutil.NewPool(t)
	org := newOrg(t, pool, "pro", "active")
	id := newMonitor(t, pool, org, 120)
	s := newScheduler(pool, scheduler.Config{})

	st, err := s.Tick(t.Context())
	if err != nil || st.Scheduled != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	if n := secondsUntilNext(t, pool, id); n < 115 || n > 120*1.1+2 {
		t.Fatalf("next check in %.1fs, want ~120s plus up to 10%% jitter", n)
	}
	if st, _ := s.Tick(t.Context()); st.Scheduled != 0 || jobCount(t, pool) != 1 {
		t.Fatalf("not due again yet: %+v", st)
	}
}

func TestOnlyEnabledLiveDueMonitorsAreScheduled(t *testing.T) {
	pool := testutil.NewPool(t)
	org := newOrg(t, pool, "pro", "active")
	due := newMonitor(t, pool, org, 60)
	paused := newMonitor(t, pool, org, 60)
	deleted := newMonitor(t, pool, org, 60)
	future := newMonitor(t, pool, org, 60)
	pool.Exec(t.Context(), `UPDATE monitors SET enabled = false WHERE id = $1`, paused)
	pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() WHERE id = $1`, deleted)
	pool.Exec(t.Context(), `UPDATE monitors SET next_check_at = now() + interval '1 hour' WHERE id = $1`, future)

	newScheduler(pool, scheduler.Config{}).Tick(t.Context())
	var scheduled []uuid.UUID
	rows, _ := pool.Query(t.Context(), `SELECT monitor_id FROM check_jobs`)
	for rows.Next() {
		var id uuid.UUID
		rows.Scan(&id)
		scheduled = append(scheduled, id)
	}
	rows.Close()
	if len(scheduled) != 1 || scheduled[0] != due {
		t.Fatalf("scheduled %v, want only %v", scheduled, due)
	}
}

// The user-stored interval can never beat the plan: the effective interval is derived from the entitlement.
func TestScheduleHonoursPlanMinimumInterval(t *testing.T) {
	pool := testutil.NewPool(t)
	cases := []struct {
		plan, status string
		want         float64
	}{
		{"free", "active", 300}, {"pro", "active", 60}, {"pro", "canceled", 300}, {"agency", "incomplete", 300},
	}
	s := newScheduler(pool, scheduler.Config{JitterFrac: 0.0001})
	for _, c := range cases {
		id := newMonitor(t, pool, newOrg(t, pool, c.plan, c.status), 60) // every monitor asks for 60s
		s.Tick(t.Context())
		if n := secondsUntilNext(t, pool, id); n < c.want-5 || n > c.want+5 {
			t.Errorf("%s/%s: next check in %.1fs, want ~%.0fs", c.plan, c.status, n, c.want)
		}
	}
}

func TestJitterBounds(t *testing.T) {
	for _, rnd := range []float64{0, 0.999999} {
		pool := testutil.NewPool(t)
		id := newMonitor(t, pool, newOrg(t, pool, "free", "active"), 300)
		s := newScheduler(pool, scheduler.Config{JitterFrac: 0.1, MaxJitter: 20 * time.Second})
		scheduler.SetRand(s, func() float64 { return rnd })
		s.Tick(t.Context())
		n := secondsUntilNext(t, pool, id)
		lo, hi := 299.0, 301.0
		if rnd > 0.5 {
			lo, hi = 318, 321 // jitter is capped at MaxJitter (20s), not 10% of 300s
		}
		if n < lo || n > hi {
			t.Errorf("rnd=%v: next in %.1fs, want %v..%v", rnd, n, lo, hi)
		}
	}
}

func TestOutstandingJobIsSkippedNotDuplicated(t *testing.T) {
	pool := testutil.NewPool(t)
	id := newMonitor(t, pool, newOrg(t, pool, "free", "active"), 300)
	pool.Exec(t.Context(), `INSERT INTO check_jobs (monitor_id, status, locked_until) VALUES ($1, 'running', now() + interval '1 minute')`, id)

	st, err := newScheduler(pool, scheduler.Config{}).Tick(t.Context())
	if err != nil || st.Scheduled != 0 || st.Skipped != 1 || jobCount(t, pool) != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	if n := secondsUntilNext(t, pool, id); n < 290 {
		t.Fatalf("schedule must still advance so the monitor is not re-examined every tick: %.0f", n)
	}
}

func TestMissedChecksAreCountedAndNoCatchUpBacklog(t *testing.T) {
	pool := testutil.NewPool(t)
	id := newMonitor(t, pool, newOrg(t, pool, "free", "active"), 300)
	pool.Exec(t.Context(), `UPDATE monitors SET next_check_at = now() - interval '3 hours'`)
	st, _ := newScheduler(pool, scheduler.Config{}).Tick(t.Context())
	if st.Missed != 1 || st.Scheduled != 1 {
		t.Fatalf("%+v", st)
	}
	if n := secondsUntilNext(t, pool, id); n < 295 {
		t.Fatalf("after downtime the next check is one interval from now, got %.0fs", n)
	}
}

func TestConcurrentSchedulersNeverDuplicateJobs(t *testing.T) {
	pool := testutil.NewPoolSize(t, 30)
	org := newOrg(t, pool, "pro", "active")
	const n = 60
	for range n {
		newMonitor(t, pool, org, 60)
	}
	var mu sync.Mutex
	total := 0
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := newScheduler(pool, scheduler.Config{Batch: 20})
			<-start
			for range 5 {
				st, err := s.Tick(t.Context())
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				total += st.Scheduled
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if total != n || jobCount(t, pool) != n {
		t.Fatalf("scheduled=%d jobs=%d, want %d each", total, jobCount(t, pool), n)
	}
}

func TestQueueDepthCapAppliesBackpressure(t *testing.T) {
	pool := testutil.NewPool(t)
	org := newOrg(t, pool, "pro", "active")
	for range 10 {
		newMonitor(t, pool, org, 60)
	}
	s := newScheduler(pool, scheduler.Config{MaxQueueDepth: 4})
	st, _ := s.Tick(t.Context())
	if st.Scheduled != 4 || jobCount(t, pool) != 4 {
		t.Fatalf("%+v jobs=%d", st, jobCount(t, pool))
	}
	st, _ = s.Tick(t.Context())
	if !st.Backpressure || st.Scheduled != 0 || jobCount(t, pool) != 4 {
		t.Fatalf("a full queue must not grow: %+v jobs=%d", st, jobCount(t, pool))
	}
	lag, err := scheduler.Lag(t.Context(), pool)
	if err != nil || lag < 0 {
		t.Fatalf("lag %v %v", lag, err)
	}
}

func TestBatchLimit(t *testing.T) {
	pool := testutil.NewPool(t)
	org := newOrg(t, pool, "pro", "active")
	for range 7 {
		newMonitor(t, pool, org, 60)
	}
	st, _ := newScheduler(pool, scheduler.Config{Batch: 3}).Tick(t.Context())
	if st.Scheduled != 3 {
		t.Fatalf("%+v", st)
	}
}
