package incidents_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/checks"
)

// timed records one successful check that took ms milliseconds, one minute after the previous check.
func (e *env) timed(t *testing.T, store *checks.Store, monitor uuid.UUID, region string, ms int) checks.Outcome {
	t.Helper()
	e.clock = e.clock.Add(time.Minute)
	r := checks.Result{CheckedAt: e.clock, FinishedAt: e.clock.Add(time.Duration(ms) * time.Millisecond), Success: true, ResponseTimeMs: &ms, Region: region}
	o, err := store.Record(t.Context(), monitor, r)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (e *env) slowAlerts(t *testing.T, event string) int {
	return e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = $1`, event)
}

func TestSlowResponsesOpenAndCloseOneSlowdown(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.recipient(t, "off@example.com", false, true)
	e.pool.Exec(t.Context(), `UPDATE monitors SET slow_threshold_ms = 1000, slow_checks = 3 WHERE id = $1`, e.monitor)

	// Two slow checks, a quick one, two slow ones: never three in a row, so nothing happens.
	for _, ms := range []int{1500, 1800, 300, 2000, 2500} {
		if o := e.timed(t, e.store, e.monitor, "", ms); o.Speed != checks.NoSpeedChange {
			t.Fatalf("%d ms: %v", ms, o.Speed)
		}
	}
	if o := e.timed(t, e.store, e.monitor, "", 3200); o.Speed != checks.BecameSlow {
		t.Fatalf("%v", o.Speed)
	}
	// A failed check is an outage matter: it neither extends nor ends the slowdown, and the monitor is still "up" after one failure.
	e.result(t, e.monitor, false)
	e.timed(t, e.store, e.monitor, "", 4100)
	if n := e.count(t, `SELECT count(*) FROM slowdowns WHERE monitor_id = $1 AND status = 'open' AND threshold_ms = 1000 AND peak_ms = 4100`, e.monitor); n != 1 {
		t.Fatalf("one open slowdown with its slowest response, got %d", n)
	}
	if e.slowAlerts(t, "slow") != 1 {
		t.Fatalf("one alert, to the one enabled recipient: %d", e.slowAlerts(t, "slow"))
	}
	if e.count(t, `SELECT count(*) FROM incidents`) != 0 {
		t.Fatal("slowness is not an outage")
	}

	for i, ms := range []int{900, 1000, 700} {
		o := e.timed(t, e.store, e.monitor, "", ms)
		if want := map[bool]checks.SpeedTransition{false: checks.NoSpeedChange, true: checks.BackToNormal}[i == 2]; o.Speed != want {
			t.Fatalf("check %d: %v", i, o.Speed)
		}
	}
	if e.count(t, `SELECT count(*) FROM slowdowns WHERE status = 'resolved' AND resolved_at > started_at`) != 1 || e.slowAlerts(t, "slow_recovery") != 1 {
		t.Fatal("the slowdown closes once, with one back-to-normal alert")
	}
}

func TestSlowResponsesAreIgnoredWhenOffOrInMaintenance(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	for range 5 {
		e.timed(t, e.store, e.monitor, "", 9000) // no threshold set: off
	}
	e.pool.Exec(t.Context(), `UPDATE monitors SET slow_threshold_ms = 1000 WHERE id = $1`, e.monitor)
	e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors)
		VALUES ($1, 'deploy', $2, 600, true)`, e.org, e.clock)
	for range 5 {
		e.timed(t, e.store, e.monitor, "", 9000)
	}
	if e.count(t, `SELECT count(*) FROM slowdowns`) != 0 {
		t.Fatal("no slowdown while alerts are off or during maintenance")
	}
}

func TestOneSlowLocationIsNotASlowdown(t *testing.T) {
	r := newRegional(t, "pro")
	m := r.monitor(t, "us", "eu", "ap")
	r.e.pool.Exec(t.Context(), `UPDATE monitors SET slow_threshold_ms = 1000, slow_checks = 2 WHERE id = $1`, m)
	round := func(us, eu, ap int) checks.SpeedTransition {
		var last checks.SpeedTransition
		for region, ms := range map[string]int{"us": us, "eu": eu, "ap": ap} {
			if o := r.e.timed(t, r.store, m, region, ms); o.Speed != checks.NoSpeedChange {
				last = o.Speed
			}
		}
		return last
	}
	for range 4 {
		if tr := round(200, 300, 5000); tr != checks.NoSpeedChange {
			t.Fatalf("Asia alone being slow is not a slowdown: %v", tr)
		}
	}
	round(200, 3000, 5000)
	if tr := round(200, 3000, 5000); tr != checks.BecameSlow {
		t.Fatalf("two of three locations slow twice in a row: %v", tr)
	}
	round(200, 300, 5000)
	if tr := round(200, 300, 5000); tr != checks.BackToNormal {
		t.Fatalf("Europe back to normal leaves only one slow location: %v", tr)
	}
}
