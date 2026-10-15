package scheduler_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/regions"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/testutil"
)

func regionMonitor(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, chosen ...string) uuid.UUID {
	t.Helper()
	if chosen == nil {
		chosen = []string{}
	}
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, regions)
		VALUES ($1, 'm', 'https://example.com', 60, $2) RETURNING id`, org, chosen).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func jobRegions(t *testing.T, pool *pgxpool.Pool, monitor uuid.UUID) map[string]bool {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT region FROM check_jobs WHERE monitor_id = $1`, monitor)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var r string
		rows.Scan(&r)
		out[r] = true
	}
	return out
}

func TestOneJobIsQueuedPerRegionTheMonitorUses(t *testing.T) {
	pool := testutil.NewPool(t)
	set, _ := regions.Parse("us,eu,ap")
	s := newScheduler(pool, scheduler.Config{}).WithRegions(set)
	pro, free := newOrg(t, pool, "pro", "active"), newOrg(t, pool, "free", "active")
	multi := regionMonitor(t, pool, pro, "eu", "ap", "us")
	none := regionMonitor(t, pool, pro)
	capped := regionMonitor(t, pool, free, "eu", "ap") // the Free plan runs one region only
	gone := regionMonitor(t, pool, pro, "mars")        // a region that is not configured: falls back to the primary

	st, err := s.Tick(t.Context())
	if err != nil || st.Scheduled != 3+1+1+1 {
		t.Fatalf("%+v %v", st, err)
	}
	for monitor, want := range map[uuid.UUID][]string{multi: {"eu", "ap", "us"}, none: {"us"}, capped: {"eu"}, gone: {"us"}} {
		got := jobRegions(t, pool, monitor)
		if len(got) != len(want) {
			t.Errorf("%v: regions %v, want %v", monitor, got, want)
		}
		for _, r := range want {
			if !got[r] {
				t.Errorf("%v: missing a job for %s (have %v)", monitor, r, got)
			}
		}
	}
	// Ticking again before the jobs are claimed adds nothing (the monitors are not due yet anyway).
	if st, _ := s.Tick(t.Context()); st.Scheduled != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestABackedUpRegionDoesNotStopTheOthers(t *testing.T) {
	pool := testutil.NewPool(t)
	set, _ := regions.Parse("us,eu")
	s := newScheduler(pool, scheduler.Config{MaxQueueDepth: 2}).WithRegions(set)
	org := newOrg(t, pool, "pro", "active")
	// eu's workers are down: its queue is already full.
	for range 2 {
		m := regionMonitor(t, pool, org, "eu")
		pool.Exec(t.Context(), `UPDATE monitors SET next_check_at = now() + interval '1 hour' WHERE id = $1`, m)
		pool.Exec(t.Context(), `INSERT INTO check_jobs (monitor_id, region) VALUES ($1, 'eu')`, m)
	}
	both := regionMonitor(t, pool, org, "us", "eu")

	st, err := s.Tick(t.Context())
	if err != nil || st.Backpressure {
		t.Fatalf("%+v %v", st, err)
	}
	got := jobRegions(t, pool, both)
	if !got["us"] || got["eu"] {
		t.Fatalf("us must still be scheduled while eu is full: %v", got)
	}
	if st.Skipped != 1 || st.Scheduled != 1 {
		t.Fatalf("%+v", st)
	}
}
