package incidents_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidents"
	"hawkeye/internal/regions"
)

// regional records results as seen from named regions.
type regional struct {
	e     *env
	store *checks.Store
}

func newRegional(t *testing.T, plan string) *regional {
	t.Helper()
	e := newEnv(t, 5)
	if plan != "pro" {
		e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = $2) WHERE organization_id = $1`, e.org, plan)
	}
	set, err := regions.Parse("us:US,eu:Europe,ap:Asia")
	if err != nil {
		t.Fatal(err)
	}
	return &regional{e: e, store: checks.NewStore(e.pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{}).WithRegions(set)}
}

func (r *regional) monitor(t *testing.T, chosen ...string) uuid.UUID {
	t.Helper()
	if chosen == nil {
		chosen = []string{}
	}
	var id uuid.UUID
	if err := r.e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, regions)
		VALUES ($1, 'Site', 'https://example.com', 60, $2) RETURNING id`, r.e.org, chosen).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *regional) record(t *testing.T, monitor uuid.UUID, region string, ok bool) checks.Outcome {
	t.Helper()
	r.e.clock = r.e.clock.Add(time.Minute)
	res := checks.Result{CheckedAt: r.e.clock, FinishedAt: r.e.clock.Add(100 * time.Millisecond), Success: ok, Region: region}
	if !ok {
		res.ErrorCategory, res.ErrorSummary = checks.CatHTTPStatus, "Received HTTP 503"
	}
	o, err := r.store.Record(t.Context(), monitor, res)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (r *regional) status(t *testing.T, monitor uuid.UUID) string {
	t.Helper()
	var s string
	r.e.pool.QueryRow(t.Context(), `SELECT current_status FROM monitors WHERE id = $1`, monitor).Scan(&s)
	return s
}

func (r *regional) incidents(t *testing.T, monitor uuid.UUID, status string) int {
	return r.e.count(t, `SELECT count(*) FROM incidents WHERE monitor_id = $1 AND status = $2`, monitor, status)
}

func TestOneRegionFailingDoesNotRaiseAnIncidentButAMajorityDoes(t *testing.T) {
	r := newRegional(t, "pro")
	m := r.monitor(t, "us", "eu", "ap")
	for _, region := range []string{"us", "eu", "ap"} {
		r.record(t, m, region, true)
	}
	if r.status(t, m) != "up" {
		t.Fatalf("status %s", r.status(t, m))
	}

	// us alone has a problem: two failures put that region down, but the monitor stays up.
	r.record(t, m, "us", false)
	o := r.record(t, m, "us", false)
	if o.Transition != checks.NoTransition || r.status(t, m) != "up" || r.incidents(t, m, "open") != 0 {
		t.Fatalf("one of three regions must not open an incident: %+v status=%s", o, r.status(t, m))
	}

	// eu agrees: two of three regions are down.
	r.record(t, m, "eu", false)
	o = r.record(t, m, "eu", false)
	if o.Transition != checks.WentDown || r.status(t, m) != "down" || r.incidents(t, m, "open") != 1 {
		t.Fatalf("a majority must open an incident: %+v status=%s", o, r.status(t, m))
	}
	var summary string
	r.e.pool.QueryRow(t.Context(), `SELECT summary FROM incidents WHERE monitor_id = $1`, m).Scan(&summary)
	if !strings.Contains(summary, "2 of 3 locations") {
		t.Fatalf("the incident should say how many locations agree: %q", summary)
	}

	// us recovers (two successes): only eu is down, below the quorum, so the monitor recovers.
	r.record(t, m, "us", true)
	o = r.record(t, m, "us", true)
	if o.Transition != checks.Recovered || r.status(t, m) != "up" || r.incidents(t, m, "resolved") != 1 || r.incidents(t, m, "open") != 0 {
		t.Fatalf("losing the majority must resolve the incident: %+v status=%s", o, r.status(t, m))
	}
}

func TestTwoRegionsMustBothAgree(t *testing.T) {
	r := newRegional(t, "pro")
	m := r.monitor(t, "us", "eu")
	r.record(t, m, "us", true)
	r.record(t, m, "eu", true)
	r.record(t, m, "us", false)
	r.record(t, m, "us", false)
	if r.status(t, m) != "up" {
		t.Fatal("one of two regions is not a majority")
	}
	r.record(t, m, "eu", false)
	if o := r.record(t, m, "eu", false); o.Transition != checks.WentDown {
		t.Fatalf("%+v", o)
	}
}

func TestMonitorWithOneNonPrimaryRegionBehavesLikeAnyOther(t *testing.T) {
	r := newRegional(t, "free")
	m := r.monitor(t, "eu")
	r.record(t, m, "eu", false)
	if o := r.record(t, m, "eu", false); o.Transition != checks.WentDown || r.incidents(t, m, "open") != 1 {
		t.Fatalf("%+v", o)
	}
	// A late result from a region it no longer uses is kept as history and changes nothing.
	o := r.record(t, m, "us", true)
	r.record(t, m, "us", true)
	if r.status(t, m) != "down" || o.Transition != checks.NoTransition {
		t.Fatalf("a result from a region the monitor does not use must not change its state: %s", r.status(t, m))
	}
	if n := r.e.count(t, `SELECT count(*) FROM check_results WHERE monitor_id = $1 AND region = 'us'`, m); n != 2 {
		t.Fatalf("recorded %d results from us", n)
	}
}

func TestADowngradeCapsTheRegionsThatCount(t *testing.T) {
	r := newRegional(t, "free") // the plan allows one region, but the monitor still lists three
	m := r.monitor(t, "us", "eu", "ap")
	r.record(t, m, "us", false)
	if o := r.record(t, m, "us", false); o.Transition != checks.WentDown {
		t.Fatalf("only the first region is checked now, so it alone decides: %+v", o)
	}
	r.record(t, m, "eu", true) // not an active region any more
	r.record(t, m, "eu", true)
	if r.status(t, m) != "down" {
		t.Fatal("results from regions beyond the plan's limit must not count")
	}
}

func TestRegionsDoNotMixWithMaintenance(t *testing.T) {
	r := newRegional(t, "pro")
	m := r.monitor(t, "us", "eu", "ap")
	r.e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'w', now() - interval '3 hours', 600, true)`, r.e.org)
	r.e.clock = time.Now().UTC().Add(-time.Hour)
	for _, region := range []string{"us", "eu", "ap"} {
		r.record(t, m, region, false)
		r.record(t, m, region, false)
	}
	if r.incidents(t, m, "open") != 0 || r.e.count(t, `SELECT count(*) FROM monitor_region_state WHERE monitor_id = $1`, m) != 0 {
		t.Fatal("maintenance must suppress incidents and leave region streaks untouched")
	}
}
