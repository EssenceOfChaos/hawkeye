package rollups_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/reports"
	"hawkeye/internal/retention"
	"hawkeye/internal/rollups"
	"hawkeye/internal/statuspage"
	"hawkeye/internal/testutil"
	"hawkeye/internal/weeklysummary"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fixture struct {
	pool     *pgxpool.Pool
	org      uuid.UUID
	monitors []uuid.UUID // the last one is deleted
	now      time.Time
}

func newOrg(t *testing.T, pool *pgxpool.Pool, plan string) uuid.UUID {
	t.Helper()
	var org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = $2`, org, plan); err != nil {
		t.Fatal(err)
	}
	return org
}

// results adds roughly one result every interval (with random gaps) for days back from now: about 10% failed, some without a response time,
// about 5% during maintenance, from two regions.
func results(t *testing.T, pool *pgxpool.Pool, monitor uuid.UUID, days, interval int) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms,
			error_category, in_maintenance, region)
		SELECT $1, t, t, ok, CASE WHEN ok THEN 200 ELSE 503 END, CASE WHEN random() < 0.1 THEN NULL ELSE (random() * 900)::int END,
			CASE WHEN ok THEN NULL ELSE 'http_status' END, random() < 0.05, CASE WHEN random() < 0.5 THEN 'primary' ELSE 'eu' END
		FROM (SELECT now() - make_interval(days => $2) + make_interval(secs => g * $3 + floor(random() * $3 / 2)) AS t, random() > 0.1 AS ok
			FROM generate_series(0, $2 * 86400 / $3) g) s
		WHERE t < now()`, monitor, days, interval); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := testutil.NewPoolSize(t, 10)
	ctx := t.Context()
	f := fixture{pool: pool, org: newOrg(t, pool, "agency"), now: time.Now().UTC()}
	for i := range 3 {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, $2, 'https://example.com', 60)
			RETURNING id`, f.org, fmt.Sprintf("site %d", i)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		f.monitors = append(f.monitors, id)
		results(t, pool, id, 40, 120+i*37)
	}
	if _, err := pool.Exec(ctx, `UPDATE monitors SET deleted_at = now() WHERE id = $1`, f.monitors[2]); err != nil {
		t.Fatal(err)
	}
	// Another organization's results must never be counted.
	other := newOrg(t, pool, "agency")
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'x', 'https://example.net', 60)
		RETURNING id`, other).Scan(&id); err != nil {
		t.Fatal(err)
	}
	results(t, pool, id, 40, 60)

	var page uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO status_pages (organization_id, slug, title, published) VALUES ($1, 'rollup-test', 'Status', true) RETURNING id`,
		f.org).Scan(&page); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO status_page_monitors (page_id, monitor_id, position) VALUES ($1, $2, 1), ($1, $3, 2)`,
		page, f.monitors[0], f.monitors[1]); err != nil {
		t.Fatal(err)
	}
	return f
}

// answers is everything that adds up results, as the app would show it at f.now.
func (f fixture) answers(t *testing.T) map[string]any {
	t.Helper()
	ctx := t.Context()
	out := map[string]any{}
	odd := f.now.Add(-17 * time.Minute) // ranges that end and start mid-hour
	for _, span := range []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 39*24*time.Hour + 41*time.Minute} {
		r := reports.Range{From: odd.Add(-span), To: odd, RetentionDays: 365}
		for _, who := range []*uuid.UUID{nil, &f.monitors[0], &f.monitors[2]} {
			label := fmt.Sprintf("%v %v", span, who != nil)
			if who != nil {
				label += " " + who.String()[:4]
			}
			u, err := reports.ComputeUptime(ctx, f.pool, f.org, who, r)
			if err != nil {
				t.Fatal(err)
			}
			out["uptime "+label] = u
			pts, bucket, err := reports.Series(ctx, f.pool, f.org, who, r)
			if err != nil {
				t.Fatal(err)
			}
			out[fmt.Sprintf("series %s (bucket %d)", label, bucket)] = pts
		}
	}
	page, err := statuspage.NewService(f.pool, plans.NewService("http://localhost")).Public(ctx, "rollup-test")
	if err != nil {
		t.Fatal(err)
	}
	out["status page"] = page.Monitors
	m, err := reports.BuildMonthly(ctx, f.pool, f.org, f.monitors[0], f.now.AddDate(0, -1, 0), f.now, 365)
	if err != nil {
		t.Fatal(err)
	}
	out["monthly"] = []any{m.Uptime, m.Days}
	w, err := weeklysummary.Build(ctx, f.pool, f.org, "o", odd.Add(-7*24*time.Hour), odd, f.now)
	if err != nil {
		t.Fatal(err)
	}
	out["weekly"] = w
	return out
}

func catchUp(t *testing.T, job *rollups.Job) {
	t.Helper()
	for range 1000 {
		more, err := job.Step(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			return
		}
	}
	t.Fatal("the job never caught up")
}

func compare(t *testing.T, want, got map[string]any) {
	t.Helper()
	for k, w := range want {
		if !reflect.DeepEqual(w, got[k]) {
			t.Errorf("%s differs with rollups:\nraw:     %+v\nrollups: %+v", k, w, got[k])
		}
	}
}

func rollupCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM check_rollups`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEveryReaderGivesTheSameAnswerWithRollups(t *testing.T) {
	f := setup(t)
	raw := f.answers(t) // rollup_state.through is NULL: raw results only

	job := rollups.NewJob(f.pool, rollups.JobConfig{Now: func() time.Time { return f.now }}, metrics.New(), quiet)
	catchUp(t, job)
	through, err := rollups.Through(t.Context(), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if want := f.now.Add(-15 * time.Minute).Truncate(time.Hour); !through.Equal(want) {
		t.Fatalf("through = %s, want %s", through, want)
	}
	if rollupCount(t, f.pool) < 1000 {
		t.Fatalf("only %d rollup rows", rollupCount(t, f.pool))
	}
	compare(t, raw, f.answers(t))
	if t.Failed() {
		return
	}

	// A result recorded late for an hour already added up (as the heartbeat sweeper does for a missed deadline once the worker is back) is
	// added on the next step.
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, error_category)
		VALUES ($1, $2, $2, false, 'missed_ping')`, f.monitors[0], f.now.Add(-50*time.Hour).Truncate(time.Hour).Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE rollup_state SET through = NULL, seen_id = (SELECT seen_id FROM rollup_state)`); err != nil {
		t.Fatal(err)
	}
	raw = f.answers(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE rollup_state SET through = $1`, through); err != nil {
		t.Fatal(err)
	}
	if _, err := job.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	compare(t, raw, f.answers(t))
}

func TestStepsAreRepeatableAndOnlyAddClosedHours(t *testing.T) {
	f := setup(t)
	now := f.now
	job := rollups.NewJob(f.pool, rollups.JobConfig{Now: func() time.Time { return now }, Chunk: 24 * time.Hour}, metrics.New(), quiet)
	catchUp(t, job)
	before := rollupCount(t, f.pool)
	var newest time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT max(hour) FROM check_rollups`).Scan(&newest); err != nil {
		t.Fatal(err)
	}
	if !newest.Before(now.Add(-15 * time.Minute).Truncate(time.Hour)) {
		t.Fatalf("an hour that hasn't settled was added up: %s", newest)
	}
	// Doing it again, from scratch, gives the same rows.
	if _, err := f.pool.Exec(t.Context(), `UPDATE rollup_state SET through = NULL, seen_id = NULL`); err != nil {
		t.Fatal(err)
	}
	catchUp(t, job)
	if after := rollupCount(t, f.pool); after != before {
		t.Fatalf("%d rows after rebuilding, %d before", after, before)
	}
	// An hour later, one more hour is added.
	now = now.Add(time.Hour)
	if _, err := job.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	through, _ := rollups.Through(t.Context(), f.pool)
	if want := now.Add(-15 * time.Minute).Truncate(time.Hour); !through.Equal(want) {
		t.Fatalf("through = %s, want %s", through, want)
	}
}

func TestTheFirstRunWithNoResultsStartsNow(t *testing.T) {
	pool := testutil.NewPool(t)
	now := time.Date(2026, 11, 16, 10, 20, 0, 0, time.UTC)
	job := rollups.NewJob(pool, rollups.JobConfig{Now: func() time.Time { return now }}, metrics.New(), quiet)
	if more, err := job.Step(t.Context()); err != nil || more {
		t.Fatalf("more %v, err %v", more, err)
	}
	through, _ := rollups.Through(t.Context(), pool)
	if want := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC); !through.Equal(want) {
		t.Fatalf("through = %s, want %s", through, want)
	}
}

func TestRetentionDeletesTotalsWithTheResults(t *testing.T) {
	pool := testutil.NewPool(t)
	ctx := context.Background()
	org := newOrg(t, pool, "free") // keeps 7 days
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'x', 'https://example.com', 300)
		RETURNING id`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	results(t, pool, id, 10, 600)
	catchUp(t, rollups.NewJob(pool, rollups.JobConfig{}, metrics.New(), quiet))
	if _, err := retention.New(pool, retention.Config{}, metrics.New(), quiet).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var oldResult, oldTotal *time.Time
	if err := pool.QueryRow(ctx, `SELECT (SELECT min(checked_at) FROM check_results), (SELECT min(hour) FROM check_rollups)`).Scan(&oldResult, &oldTotal); err != nil {
		t.Fatal(err)
	}
	if oldResult == nil || oldTotal == nil {
		t.Fatal("everything was deleted")
	}
	if oldTotal.Add(time.Hour).Before(*oldResult) {
		t.Fatalf("totals from %s outlived the results (oldest %s)", oldTotal, oldResult)
	}
	if time.Since(*oldTotal) > 8*24*time.Hour {
		t.Fatalf("totals from %s were kept past the 7-day history", oldTotal)
	}
}

// A monitor checked every 30 seconds keeps 30 days of individual results and its totals for the plan's whole history: uptime over 40 days is
// unchanged after retention, a rebuild keeps the totals whose results are gone, and nothing is deleted while totals aren't built.
func TestSubMinuteMonitorsKeepTotalsLongerThanResults(t *testing.T) {
	pool := testutil.NewPoolSize(t, 10)
	ctx := context.Background()
	// The interval change isn't released yet: allow 30-second checks in this test database only.
	if _, err := pool.Exec(ctx, `ALTER TABLE monitors DROP CONSTRAINT monitors_check_interval_seconds_check`); err != nil {
		t.Fatal(err)
	}
	org := newOrg(t, pool, "agency")
	var fast, slow uuid.UUID
	for _, m := range []struct {
		id       *uuid.UUID
		interval int
	}{{&fast, 30}, {&slow, 60}} {
		if err := pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'x', 'https://example.com', $2)
			RETURNING id`, org, m.interval).Scan(m.id); err != nil {
			t.Fatal(err)
		}
		results(t, pool, *m.id, 40, 900)
	}
	now := time.Now().UTC()
	r := reports.Range{From: now.Add(-39 * 24 * time.Hour).Truncate(time.Hour), To: now, RetentionDays: 365}
	uptime := func() reports.Uptime {
		t.Helper()
		u, err := reports.ComputeUptime(ctx, pool, org, &fast, r)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	oldest := func(id uuid.UUID) time.Time {
		t.Helper()
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT min(checked_at) FROM check_results WHERE monitor_id = $1`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	keep := retention.New(pool, retention.Config{}, metrics.New(), quiet)

	// No totals yet: nothing may be deleted.
	if _, err := keep.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(oldest(fast)) < 35*24*time.Hour {
		t.Fatalf("results were deleted before their totals existed (oldest %s)", oldest(fast))
	}

	want := uptime()
	job := rollups.NewJob(pool, rollups.JobConfig{}, metrics.New(), quiet)
	catchUp(t, job)
	if _, err := keep.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	limit := now.Add(-rollups.RawRetention).Truncate(time.Hour)
	if got := oldest(fast); got.Before(limit) {
		t.Fatalf("the 30-second monitor kept a result from %s, older than %s", got, limit)
	}
	if got := oldest(slow); time.Since(got) < 35*24*time.Hour {
		t.Fatalf("the 60-second monitor lost results (oldest %s)", got)
	}
	if got := uptime(); !reflect.DeepEqual(got, want) {
		t.Fatalf("uptime over 39 days changed when old results went:\nbefore %+v\nafter  %+v", want, got)
	}

	// Rebuilding every total from scratch must not lose the ones whose results are gone.
	if _, err := pool.Exec(ctx, `UPDATE rollup_state SET through = NULL, seen_id = NULL`); err != nil {
		t.Fatal(err)
	}
	catchUp(t, job)
	if got := uptime(); !reflect.DeepEqual(got, want) {
		t.Fatalf("uptime changed after a rebuild:\nbefore %+v\nafter  %+v", want, got)
	}
}
