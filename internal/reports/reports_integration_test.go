package reports_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/reports"
	"hawkeye/internal/testutil"
)

type env struct {
	pool *pgxpool.Pool
	org  uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{pool: testutil.NewPool(t)}
	e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&e.org)
	return e
}

func (e *env) monitor(t *testing.T, org uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds)
		VALUES ($1, $2, 'https://example.com', 60) RETURNING id`, org, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// seed inserts n results spaced `every` apart, the newest `ago` before now. failEvery>0 makes every
// failEvery-th result a failure; successes have response time ms.
//
// "now" is the TEST PROCESS's clock, passed in as a parameter, not the database's now(). The reports are then asked about a window built from
// time.Now() in this same process (rng below), so every seeded result is guaranteed to be at or before the window's end. Seeding from the
// database's clock instead made the two clocks race: when the database's clock was even slightly ahead (Docker's VM clock drifts and re-syncs),
// the newest result fell just past the end of the window and a count came up one short.
func (e *env) seed(t *testing.T, monitor uuid.UUID, n int, every, ago time.Duration, failEvery int, ms int) {
	t.Helper()
	now := time.Now().UTC()
	_, err := e.pool.Exec(t.Context(), `INSERT INTO check_results
		(monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms, error_category, error_summary)
		SELECT $1, ts, ts, ok, CASE WHEN ok THEN 200 ELSE 503 END, CASE WHEN ok THEN $6::int END,
			CASE WHEN ok THEN NULL ELSE 'http_status' END, CASE WHEN ok THEN NULL ELSE 'Received HTTP 503' END
		FROM (SELECT $7::timestamptz - $3::interval - ($2::interval * g) AS ts, NOT ($4::int > 0 AND g % $4::int = 0) AS ok FROM generate_series(0, $5::int - 1) g) s`,
		monitor, every, ago, failEvery, n, ms, now)
	if err != nil {
		t.Fatal(err)
	}
}

func rng(t *testing.T, preset string) reports.Range {
	t.Helper()
	r, err := reports.ResolveRange(time.Now(), preset, nil, nil, 365)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestUptimeCountsOnlyCompletedChecksInTheWindow(t *testing.T) {
	e := newEnv(t)
	m := e.monitor(t, e.org, "a")
	// 100 results, every 6 minutes over the last 10 hours; every 10th (g=0,10,...,90) fails => 10 failures.
	e.seed(t, m, 100, 6*time.Minute, time.Minute, 10, 200)
	// Old results outside the 24h window must not count.
	e.seed(t, m, 50, time.Minute, 48*time.Hour, 0, 200)

	u, err := reports.ComputeUptime(t.Context(), e.pool, e.org, &m, rng(t, "24h"))
	if err != nil {
		t.Fatal(err)
	}
	if u.TotalChecks != 100 || u.FailedChecks != 10 || u.SuccessfulChecks != 90 || u.InsufficientData ||
		u.UptimePercent == nil || *u.UptimePercent != 90 || u.AvgResponseMs == nil || *u.AvgResponseMs != 200 || u.Method != "check_based" {
		t.Fatalf("%+v", u)
	}
	u7, _ := reports.ComputeUptime(t.Context(), e.pool, e.org, &m, rng(t, "7d"))
	if u7.TotalChecks != 150 {
		t.Fatalf("7d should include the older results: %d", u7.TotalChecks)
	}
}

func TestUptimeWithTooLittleDataGivesNoPercentage(t *testing.T) {
	e := newEnv(t)
	m := e.monitor(t, e.org, "new")
	e.seed(t, m, 3, time.Minute, time.Minute, 0, 100)
	u, _ := reports.ComputeUptime(t.Context(), e.pool, e.org, &m, rng(t, "24h"))
	if !u.InsufficientData || u.UptimePercent != nil || u.TotalChecks != 3 || u.MinChecks != 5 {
		t.Fatalf("%+v", u)
	}
	empty := e.monitor(t, e.org, "empty")
	u, _ = reports.ComputeUptime(t.Context(), e.pool, e.org, &empty, rng(t, "24h"))
	if !u.InsufficientData || u.UptimePercent != nil || u.AvgResponseMs != nil {
		t.Fatalf("%+v", u)
	}
}

func TestOrganizationUptimeExcludesDeletedMonitorsAndOtherTenants(t *testing.T) {
	e := newEnv(t)
	a, deleted := e.monitor(t, e.org, "a"), e.monitor(t, e.org, "gone")
	var other uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('x') RETURNING id`).Scan(&other)
	b := e.monitor(t, other, "b")
	e.seed(t, a, 10, time.Minute, time.Minute, 0, 100)
	e.seed(t, deleted, 20, time.Minute, time.Minute, 1, 100) // all failures
	e.seed(t, b, 30, time.Minute, time.Minute, 0, 100)
	e.pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() WHERE id = $1`, deleted)

	u, _ := reports.ComputeUptime(t.Context(), e.pool, e.org, nil, rng(t, "24h"))
	if u.TotalChecks != 10 || *u.UptimePercent != 100 {
		t.Fatalf("org uptime must only reflect its live monitors: %+v", u)
	}
	// Asking for another tenant's monitor ID within this organization yields nothing.
	leak, _ := reports.ComputeUptime(t.Context(), e.pool, e.org, &b, rng(t, "24h"))
	if leak.TotalChecks != 0 {
		t.Fatalf("cross-tenant read: %+v", leak)
	}
}

func TestSeriesBucketsAveragesAndGaps(t *testing.T) {
	e := newEnv(t)
	m := e.monitor(t, e.org, "a")
	// 2 hours of checks every minute at 100ms, then a 3 hour gap, then 1 hour of failures only.
	e.seed(t, m, 120, time.Minute, 6*time.Hour, 0, 100)
	e.seed(t, m, 60, time.Minute, 0, 1, 0)

	r := rng(t, "24h")
	pts, bucket, err := reports.Series(t.Context(), e.pool, e.org, &m, r)
	if err != nil || bucket != 900 {
		t.Fatalf("bucket=%d err=%v", bucket, err)
	}
	if len(pts) == 0 || len(pts) > reports.MaxPoints {
		t.Fatalf("points=%d", len(pts))
	}
	checks, failed := 0, 0
	var sawFailureOnly bool
	for i, p := range pts {
		checks += p.Checks
		failed += p.Failed
		if p.Failed == p.Checks {
			sawFailureOnly = true
			if p.AvgMs != nil || p.MaxMs != nil {
				t.Fatalf("a bucket with only failures has no response time: %+v", p)
			}
		} else if p.AvgMs == nil || *p.AvgMs != 100 {
			t.Fatalf("avg over successes should be 100: %+v", p)
		}
		if i > 0 && !p.Time.After(pts[i-1].Time) {
			t.Fatal("points must be ordered")
		}
	}
	if checks != 180 || failed != 60 || !sawFailureOnly {
		t.Fatalf("checks=%d failed=%d", checks, failed)
	}
	// The 3-hour gap must show as missing buckets, not as zeros.
	var maxGap time.Duration
	for i := 1; i < len(pts); i++ {
		if g := pts[i].Time.Sub(pts[i-1].Time); g > maxGap {
			maxGap = g
		}
	}
	if maxGap < 2*time.Hour {
		t.Fatalf("gap not preserved: %v", maxGap)
	}
}

func TestOrganizationSeriesAveragesAcrossMonitors(t *testing.T) {
	e := newEnv(t)
	a, b := e.monitor(t, e.org, "a"), e.monitor(t, e.org, "b")
	e.seed(t, a, 10, time.Minute, time.Minute, 0, 100)
	e.seed(t, b, 10, time.Minute, time.Minute, 0, 300)
	pts, _, _ := reports.Series(t.Context(), e.pool, e.org, nil, rng(t, "24h"))
	total, weighted := 0, 0
	for _, p := range pts {
		total += p.Checks
		weighted += *p.AvgMs * p.Checks
	}
	if total != 20 || weighted/total < 190 || weighted/total > 210 {
		t.Fatalf("total=%d mean=%d", total, weighted/total)
	}
}

func TestResultsPaginationOrderAndRange(t *testing.T) {
	e := newEnv(t)
	m := e.monitor(t, e.org, "a")
	e.seed(t, m, 25, time.Minute, time.Minute, 5, 150)
	r := rng(t, "24h")

	page1, total, err := reports.Results(t.Context(), e.pool, e.org, m, r, 10, 0)
	if err != nil || total != 25 || len(page1) != 10 {
		t.Fatalf("%d %d %v", len(page1), total, err)
	}
	for i := 1; i < len(page1); i++ {
		if page1[i].CheckedAt.After(page1[i-1].CheckedAt) {
			t.Fatal("newest first")
		}
	}
	page3, _, _ := reports.Results(t.Context(), e.pool, e.org, m, r, 10, 20)
	if len(page3) != 5 {
		t.Fatalf("last page: %d", len(page3))
	}
	var fail *reports.Result
	for i := range page1 {
		if !page1[i].Success {
			fail = &page1[i]
		}
	}
	if fail == nil || fail.HTTPStatusCode == nil || *fail.HTTPStatusCode != 503 || fail.ErrorCategory == nil || *fail.ErrorCategory != "http_status" || fail.ResponseTimeMs != nil {
		t.Fatalf("failure row: %+v", fail)
	}
	narrow, _ := reports.ResolveRange(time.Now(), "", ptr(time.Now().Add(-10*time.Minute)), ptr(time.Now()), 365)
	_, n, _ := reports.Results(t.Context(), e.pool, e.org, m, narrow, 100, 0)
	if n == 0 || n >= 25 {
		t.Fatalf("time-range filter: %d", n)
	}
}

func ptr[T any](v T) *T { return &v }

func TestDashboardSummaryCountsAndOpenIncidents(t *testing.T) {
	e := newEnv(t)
	up, down, pending, paused := e.monitor(t, e.org, "up"), e.monitor(t, e.org, "down"), e.monitor(t, e.org, "pending"), e.monitor(t, e.org, "paused")
	deleted := e.monitor(t, e.org, "deleted")
	e.pool.Exec(t.Context(), `UPDATE monitors SET current_status='up' WHERE id=$1`, up)
	e.pool.Exec(t.Context(), `UPDATE monitors SET current_status='down' WHERE id=$1`, down)
	e.pool.Exec(t.Context(), `UPDATE monitors SET enabled=false, current_status='up' WHERE id=$1`, paused) // paused wins over status
	e.pool.Exec(t.Context(), `UPDATE monitors SET deleted_at=now(), current_status='down' WHERE id=$1`, deleted)
	e.pool.Exec(t.Context(), `INSERT INTO incidents (monitor_id, started_at) VALUES ($1, now()), ($2, now())`, down, deleted)
	_ = pending
	e.seed(t, up, 10, time.Minute, time.Minute, 0, 120)

	s, err := reports.DashboardSummary(t.Context(), e.pool, e.org, rng(t, "24h"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.Monitors != (reports.MonitorCounts{Total: 4, Up: 1, Down: 1, Pending: 1, Paused: 1}) || s.OpenIncidents != 1 || s.Uptime.TotalChecks != 10 {
		t.Fatalf("%+v", s)
	}
}

func TestDashboardListsCertificatesNeedingAttentionSoonestFirst(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	set := func(id uuid.UUID, d time.Duration) {
		e.pool.Exec(t.Context(), `UPDATE monitors SET ssl_expires_at = $2 WHERE id = $1`, id, now.Add(d))
	}
	soon, expired, month, far := e.monitor(t, e.org, "soon"), e.monitor(t, e.org, "expired"), e.monitor(t, e.org, "month"), e.monitor(t, e.org, "far")
	paused, deleted, none := e.monitor(t, e.org, "paused"), e.monitor(t, e.org, "deleted"), e.monitor(t, e.org, "none")
	_ = none
	set(soon, 3*24*time.Hour+time.Hour) // padded: Postgres truncates to µs, so an exact 72h would floor to 2 days
	set(expired, -2*24*time.Hour)
	set(month, 29*24*time.Hour)
	set(far, 90*24*time.Hour)
	set(paused, time.Hour)
	set(deleted, time.Hour)
	e.pool.Exec(t.Context(), `UPDATE monitors SET enabled = false WHERE id = $1`, paused)
	e.pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() WHERE id = $1`, deleted)
	var otherOrg uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('other') RETURNING id`).Scan(&otherOrg)
	set(e.monitor(t, otherOrg, "someone else's"), time.Hour)

	s, err := reports.DashboardSummary(t.Context(), e.pool, e.org, rng(t, "24h"), now)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range s.Certificates {
		got = append(got, c.MonitorName+":"+c.Status)
	}
	if want := "expired:expired soon:expiring month:expiring"; strings.Join(got, " ") != want {
		t.Fatalf("certificates = %v, want %q (no far-away, paused, deleted, other-tenant or certificate-less monitors)", got, want)
	}
	if s.Certificates[1].DaysRemaining != 3 {
		t.Fatalf("days remaining %d", s.Certificates[1].DaysRemaining)
	}
}
