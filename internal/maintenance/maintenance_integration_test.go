package maintenance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidents"
	"hawkeye/internal/maintenance"
	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/reports"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

type env struct {
	pool  *pgxpool.Pool
	svc   *maintenance.Service
	store *checks.Store
	plans *plans.Service
	org   uuid.UUID
	mon   uuid.UUID
}

func newEnv(t *testing.T, plan string) *env {
	t.Helper()
	pool := testutil.NewPoolSize(t, 5)
	p := plans.NewService("http://app.test")
	e := &env{pool: pool, svc: maintenance.NewService(pool, p), plans: p,
		store: checks.NewStore(pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{})}
	pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&e.org)
	pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code=$2`, e.org, plan)
	e.mon = e.monitor(t, e.org)
	return e
}

func (e *env) monitor(t *testing.T, org uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds)
		VALUES ($1, 'Site', 'https://example.com', 60) RETURNING id`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) inMaintenance(t *testing.T, monitor uuid.UUID, at time.Time) bool {
	t.Helper()
	var v bool
	if err := e.pool.QueryRow(t.Context(), `SELECT in_maintenance(organization_id, id, $2) FROM monitors WHERE id = $1`, monitor, at).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *env) record(t *testing.T, at time.Time, ok bool) {
	t.Helper()
	r := checks.Result{CheckedAt: at, FinishedAt: at.Add(100 * time.Millisecond), Success: ok}
	if !ok {
		r.ErrorCategory, r.ErrorSummary = checks.CatHTTPStatus, "Received HTTP 503"
	}
	if _, err := e.store.Record(t.Context(), e.mon, r); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var day = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // a Monday

func TestWindowRuleOneOffDailyWeeklyAndScope(t *testing.T) {
	e := newEnv(t, "pro")
	other := e.monitor(t, e.org)

	mk := func(starts time.Time, mins int, repeat string, all bool, monitors ...uuid.UUID) {
		t.Helper()
		var id uuid.UUID
		if err := e.pool.QueryRow(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, repeat, all_monitors)
			VALUES ($1, 'w', $2, $3, $4, $5) RETURNING id`, e.org, starts, mins, repeat, all).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, m := range monitors {
			e.pool.Exec(t.Context(), `INSERT INTO maintenance_window_monitors VALUES ($1, $2)`, id, m)
		}
	}
	// One-off on e.mon only: Monday 10:00-11:00.
	mk(day.Add(10*time.Hour), 60, "none", false, e.mon)
	for at, want := range map[time.Time]bool{
		day.Add(9*time.Hour + 59*time.Minute):      false,
		day.Add(10 * time.Hour):                    true,
		day.Add(10*time.Hour + 59*time.Minute):     true,
		day.Add(11 * time.Hour):                    false, // the end is exclusive
		day.Add(24*time.Hour + 10*time.Hour):       false, // does not repeat
		day.Add(7*24*time.Hour + 10*time.Hour + 1): false,
	} {
		if got := e.inMaintenance(t, e.mon, at); got != want {
			t.Errorf("one-off at %v: got %v want %v", at, got, want)
		}
	}
	if e.inMaintenance(t, other, day.Add(10*time.Hour+time.Minute)) {
		t.Error("a window for one monitor must not cover another")
	}

	// Daily 02:00-03:00 on every monitor, starting Monday.
	mk(day.Add(2*time.Hour), 60, "daily", true)
	for at, want := range map[time.Time]bool{
		day.Add(1 * time.Hour):                               false, // before the first occurrence
		day.Add(2*time.Hour + 30*time.Minute):                true,
		day.Add(24*time.Hour + 2*time.Hour + 59*time.Minute): true,
		day.Add(24*time.Hour + 3*time.Hour):                  false,
		day.Add(30*24*time.Hour + 2*time.Hour):               true,
	} {
		if e.inMaintenance(t, other, at) != want || e.inMaintenance(t, e.mon, at) != want {
			t.Errorf("daily at %v: want %v", at, want)
		}
	}

	// Weekly, Sunday-ish: starts Wednesday 12:00 for 2 hours.
	mk(day.Add(2*24*time.Hour+12*time.Hour), 120, "weekly", true)
	for at, want := range map[time.Time]bool{
		day.Add(2*24*time.Hour + 13*time.Hour):  true,
		day.Add(9*24*time.Hour + 13*time.Hour):  true,
		day.Add(10*24*time.Hour + 13*time.Hour): false,
		day.Add(9*24*time.Hour + 14*time.Hour):  false,
	} {
		if got := e.inMaintenance(t, other, at); got != want {
			t.Errorf("weekly at %v: got %v want %v", at, got, want)
		}
	}

	// A window in another organization never applies.
	var otherOrg uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('x') RETURNING id`).Scan(&otherOrg)
	e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'x', $2, 600, true)`, otherOrg, day.Add(100*24*time.Hour))
	if e.inMaintenance(t, e.mon, day.Add(100*24*time.Hour+time.Hour)) {
		t.Error("another organization's window leaked")
	}
}

func TestChecksDuringMaintenanceRaiseNoIncidentAndDoNotCountTowardsUptime(t *testing.T) {
	e := newEnv(t, "pro")
	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Minute)
	e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'deploy', $2, 30, true)`,
		e.org, base.Add(10*time.Minute))

	e.record(t, base.Add(1*time.Minute), true)
	e.record(t, base.Add(2*time.Minute), true) // up
	for i := 11; i <= 14; i++ {
		e.record(t, base.Add(time.Duration(i)*time.Minute), false) // four failures inside the window
	}
	var status string
	var failures int
	e.pool.QueryRow(t.Context(), `SELECT current_status, consecutive_failures FROM monitors WHERE id = $1`, e.mon).Scan(&status, &failures)
	if status != "up" || failures != 0 || e.count(t, `SELECT count(*) FROM incidents`) != 0 {
		t.Fatalf("maintenance must not change health or open incidents: %s %d", status, failures)
	}
	if n := e.count(t, `SELECT count(*) FROM check_results WHERE monitor_id = $1 AND in_maintenance`, e.mon); n != 4 {
		t.Fatalf("flagged %d results, want 4 (they are still recorded)", n)
	}

	u, err := reports.ComputeUptime(t.Context(), e.pool, e.org, &e.mon, reports.Range{From: base, To: base.Add(2 * time.Hour)})
	if err != nil || u.TotalChecks != 2 || u.FailedChecks != 0 {
		t.Fatalf("uptime must ignore maintenance checks: %+v %v", u, err)
	}

	// After the window, failures count again and open an incident as usual.
	e.record(t, base.Add(45*time.Minute), false)
	e.record(t, base.Add(46*time.Minute), false)
	if n := e.count(t, `SELECT count(*) FROM incidents WHERE monitor_id = $1`, e.mon); n != 1 {
		t.Fatalf("incidents after maintenance = %d, want 1", n)
	}
}

func TestMonitorReportsWhetherItIsInMaintenance(t *testing.T) {
	e := newEnv(t, "pro")
	svc := monitors.NewService(e.pool, e.plans)
	e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'now', now() - interval '5 minutes', 60, true)`, e.org)
	m, err := svc.Get(t.Context(), e.org, e.mon)
	if err != nil || !m.InMaintenance {
		t.Fatalf("%+v %v", m, err)
	}
	e.pool.Exec(t.Context(), `DELETE FROM maintenance_windows`)
	if m, _ = svc.Get(t.Context(), e.org, e.mon); m.InMaintenance {
		t.Fatal("no window, no maintenance")
	}
}

func TestCreateListDeleteAndValidation(t *testing.T) {
	e := newEnv(t, "pro")
	soon := time.Now().Add(time.Hour)

	w, err := e.svc.Create(t.Context(), e.org, maintenance.Input{Name: " Deploy ", StartsAt: soon, DurationMinutes: 30, MonitorIDs: []uuid.UUID{e.mon, e.mon}})
	if err != nil || w.Name != "Deploy" || w.Status != maintenance.StatusUpcoming || w.NextStart == nil || len(w.MonitorIDs) != 1 || w.Repeat != "none" {
		t.Fatalf("%+v %v", w, err)
	}
	active, err := e.svc.Create(t.Context(), e.org, maintenance.Input{Name: "now", StartsAt: time.Now().Add(-5 * time.Minute), DurationMinutes: 60, AllMonitors: true, Repeat: "daily"})
	if err != nil || active.Status != maintenance.StatusActive || active.NextStart == nil || !active.NextStart.After(time.Now()) || len(active.MonitorIDs) != 0 {
		t.Fatalf("%+v %v", active, err)
	}
	list, _ := e.svc.List(t.Context(), e.org)
	if len(list) != 2 {
		t.Fatalf("%d windows", len(list))
	}

	other := newEnv(t, "pro")
	otherMon := other.mon
	bad := map[string]maintenance.Input{
		"name":             {Name: " ", StartsAt: soon, DurationMinutes: 30, AllMonitors: true},
		"duration_minutes": {Name: "x", StartsAt: soon, DurationMinutes: 0, AllMonitors: true},
		"repeat":           {Name: "x", StartsAt: soon, DurationMinutes: 30, Repeat: "monthly", AllMonitors: true},
		"starts_at":        {Name: "x", StartsAt: time.Now().Add(-2 * time.Hour), DurationMinutes: 30, AllMonitors: true}, // already over
		"monitor_ids":      {Name: "x", StartsAt: soon, DurationMinutes: 30},
	}
	for field, in := range bad {
		var ve *validation.Error
		if _, err := e.svc.Create(t.Context(), e.org, in); !errors.As(err, &ve) || ve.Fields[field] == "" {
			t.Errorf("%s: %v", field, err)
		}
	}
	var ve *validation.Error
	if _, err := e.svc.Create(t.Context(), e.org, maintenance.Input{Name: "x", StartsAt: soon, DurationMinutes: 1440, Repeat: "daily", AllMonitors: true}); !errors.As(err, &ve) || ve.Fields["duration_minutes"] == "" {
		t.Errorf("a daily window of 24 h would never end: %v", err)
	}
	if _, err := e.svc.Create(t.Context(), e.org, maintenance.Input{Name: "x", StartsAt: soon, DurationMinutes: 30, MonitorIDs: []uuid.UUID{otherMon}}); !errors.As(err, &ve) || ve.Fields["monitor_ids"] == "" {
		t.Errorf("another organization's monitor must be refused: %v", err)
	}

	if err := e.svc.Delete(t.Context(), other.org, w.ID); !errors.Is(err, maintenance.ErrNotFound) {
		t.Fatalf("deleting another organization's window: %v", err)
	}
	if err := e.svc.Delete(t.Context(), e.org, w.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = e.svc.List(t.Context(), e.org); len(list) != 1 {
		t.Fatalf("%d windows after delete", len(list))
	}
}

func TestFreePlanCannotCreateButCanStillListAndDelete(t *testing.T) {
	e := newEnv(t, "free")
	var le *plans.LimitError
	if _, err := e.svc.Create(t.Context(), e.org, maintenance.Input{Name: "x", StartsAt: time.Now().Add(time.Hour), DurationMinutes: 30, AllMonitors: true}); !errors.As(err, &le) || le.Kind != plans.LimitMaintenance || le.Next == nil {
		t.Fatalf("want a plan limit pointing at an upgrade, got %v", err)
	}
	// A window that exists (for example after a downgrade) can still be seen and removed.
	var id uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'old', now(), 10, true) RETURNING id`, e.org).Scan(&id)
	if list, err := e.svc.List(t.Context(), e.org); err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	if err := e.svc.Delete(t.Context(), e.org, id); err != nil {
		t.Fatal(err)
	}
}

func TestLimitOfWindowsPerOrganization(t *testing.T) {
	e := newEnv(t, "pro")
	for range maintenance.MaxWindowsPerOrganization {
		if _, err := e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'w', now(), 10, true)`, e.org); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Create(t.Context(), e.org, maintenance.Input{Name: "x", StartsAt: time.Now().Add(time.Hour), DurationMinutes: 30, AllMonitors: true}); !errors.Is(err, maintenance.ErrTooMany) {
		t.Fatalf("%v", err)
	}
}
