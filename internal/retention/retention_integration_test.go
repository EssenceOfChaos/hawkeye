package retention_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/metrics"
	"hawkeye/internal/retention"
	"hawkeye/internal/testutil"
)

type env struct {
	pool *pgxpool.Pool
}

func newEnv(t *testing.T) *env { return &env{pool: testutil.NewPoolSize(t, 10)} }

func (e *env) org(t *testing.T, plan, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&id)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, $3 FROM plans WHERE code = $2`, id, plan, status); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) monitor(t *testing.T, org uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'm', 'https://example.com', 300) RETURNING id`, org).Scan(&id)
	return id
}

// results inserts n results aged `days` days.
func (e *env) results(t *testing.T, m uuid.UUID, n, days int) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO check_results (monitor_id, checked_at, finished_at, success)
		SELECT $1, now() - make_interval(days => $3) - make_interval(secs => g), now(), true FROM generate_series(1, $2) g`, m, n, days); err != nil {
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

func (e *env) job(batch int) *retention.Job {
	return retention.New(e.pool, retention.Config{Batch: batch, Pause: 0}, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func (e *env) resultsOf(t *testing.T, m uuid.UUID) int {
	return e.count(t, `SELECT count(*) FROM check_results WHERE monitor_id = $1`, m)
}

func TestResultsAreKeptPerPlanWindow(t *testing.T) {
	e := newEnv(t)
	free, pro := e.monitor(t, e.org(t, "free", "active")), e.monitor(t, e.org(t, "pro", "active"))
	for _, m := range []uuid.UUID{free, pro} {
		e.results(t, m, 10, 3)   // inside every window
		e.results(t, m, 10, 10)  // beyond free (7d), inside pro (90d)
		e.results(t, m, 10, 100) // beyond pro
	}
	st, err := e.job(1000).RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if e.resultsOf(t, free) != 10 || e.resultsOf(t, pro) != 20 || st.CheckResults != 20+10 {
		t.Fatalf("free=%d pro=%d stats=%+v", e.resultsOf(t, free), e.resultsOf(t, pro), st)
	}
	if again, _ := e.job(1000).RunOnce(t.Context()); again.CheckResults != 0 {
		t.Fatalf("a second pass must be a no-op: %+v", again)
	}
}

func TestDowngradeShrinksTheWindowButKeepsMonitors(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "pro", "active")
	m1, m2 := e.monitor(t, org), e.monitor(t, org)
	e.results(t, m1, 5, 30)
	e.pool.Exec(t.Context(), `INSERT INTO incidents (monitor_id, started_at) VALUES ($1, now() - interval '40 days')`, m2) // open

	e.job(100).RunOnce(t.Context())
	if e.resultsOf(t, m1) != 5 {
		t.Fatal("pro keeps 30-day-old results")
	}
	e.pool.Exec(t.Context(), `UPDATE subscriptions SET status = 'canceled'`) // falls back to the 7-day free window
	e.job(100).RunOnce(t.Context())
	if e.resultsOf(t, m1) != 0 {
		t.Fatal("after downgrade results beyond 7 days are removed")
	}
	if e.count(t, `SELECT count(*) FROM monitors WHERE deleted_at IS NULL`) != 2 {
		t.Fatal("a downgrade must never delete monitors")
	}
	if e.count(t, `SELECT count(*) FROM incidents WHERE status = 'open'`) != 1 {
		t.Fatal("an unresolved incident must never be deleted")
	}
}

func TestOnlyOldResolvedIncidentsAreRemoved(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "free", "active")
	m := e.monitor(t, org)
	var oldResolved, recentResolved, oldOpen uuid.UUID
	ins := func(startedAgo, resolvedAgo string, dest *uuid.UUID) {
		q := `INSERT INTO incidents (monitor_id, started_at, resolved_at, status)
			VALUES ($1, now() - $2::interval, CASE WHEN $3::text = '' THEN NULL ELSE now() - $3::interval END, CASE WHEN $3::text = '' THEN 'open' ELSE 'resolved' END) RETURNING id`
		if err := e.pool.QueryRow(t.Context(), q, m, startedAgo, resolvedAgo).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	ins("31 days", "30 days", &oldResolved)
	ins("2 days", "1 day", &recentResolved)
	// An open incident is never removed; it needs its own monitor because only one may be open per monitor.
	m2 := e.monitor(t, org)
	e.pool.QueryRow(t.Context(), `INSERT INTO incidents (monitor_id, started_at) VALUES ($1, now() - interval '60 days') RETURNING id`, m2).Scan(&oldOpen)
	// Deliveries belong to the incident and go with it.
	var sid uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination, verified_at) VALUES ($1,'email','a@example.com', now()) RETURNING id`, org).Scan(&sid)
	e.pool.Exec(t.Context(), `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type) VALUES ($1,$2,'outage')`, oldResolved, sid)

	st, _ := e.job(100).RunOnce(t.Context())
	if st.Incidents != 1 {
		t.Fatalf("%+v", st)
	}
	for id, want := range map[uuid.UUID]int{oldResolved: 0, recentResolved: 1, oldOpen: 1} {
		if got := e.count(t, `SELECT count(*) FROM incidents WHERE id = $1`, id); got != want {
			t.Errorf("incident %s: have %d want %d", id, got, want)
		}
	}
	if e.count(t, `SELECT count(*) FROM notification_deliveries`) != 0 {
		t.Fatal("deliveries of a removed incident must go with it")
	}
}

func TestLiveMonitorsSurviveEvenWhenAllHistoryAgesOut(t *testing.T) {
	e := newEnv(t)
	m := e.monitor(t, e.org(t, "free", "active"))
	e.results(t, m, 20, 200)
	e.job(100).RunOnce(t.Context())
	if e.resultsOf(t, m) != 0 || e.count(t, `SELECT count(*) FROM monitors WHERE id = $1 AND deleted_at IS NULL`, m) != 1 {
		t.Fatal("the monitor must remain")
	}
}

func TestDeletedMonitorsArePurgedAfterGrace(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "pro", "active")
	recent, old, live := e.monitor(t, org), e.monitor(t, org), e.monitor(t, org)
	e.pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() - interval '5 days' WHERE id = $1`, recent)
	e.pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() - interval '40 days' WHERE id = $1`, old)
	e.results(t, old, 5, 1)

	st, _ := e.job(100).RunOnce(t.Context())
	if st.Monitors != 1 {
		t.Fatalf("%+v", st)
	}
	if e.count(t, `SELECT count(*) FROM monitors WHERE id = $1`, old) != 0 || e.resultsOf(t, old) != 0 {
		t.Fatal("old deleted monitor and its data should be purged")
	}
	if e.count(t, `SELECT count(*) FROM monitors WHERE id IN ($1, $2)`, recent, live) != 2 {
		t.Fatal("recently deleted and live monitors must remain")
	}
}

func TestBatchingDeletesEverythingInSmallStatements(t *testing.T) {
	e := newEnv(t)
	m := e.monitor(t, e.org(t, "free", "active"))
	e.results(t, m, 1050, 30)
	e.results(t, m, 7, 1)
	st, err := e.job(100).RunOnce(t.Context())
	if err != nil || st.CheckResults != 1050 || e.resultsOf(t, m) != 7 {
		t.Fatalf("%+v %v remaining=%d", st, err, e.resultsOf(t, m))
	}
}

func TestOnlyOnePassRunsAtATime(t *testing.T) {
	e := newEnv(t)
	m := e.monitor(t, e.org(t, "free", "active"))
	e.results(t, m, 5, 30)

	holder, err := e.pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	var got bool
	holder.QueryRow(t.Context(), `SELECT pg_try_advisory_lock(1213680466)`).Scan(&got)
	if !got {
		t.Fatal("could not take the lock")
	}
	st, err := e.job(100).RunOnce(t.Context())
	if err != nil || !st.Skipped || e.resultsOf(t, m) != 5 {
		t.Fatalf("a pass must skip while another holds the lock: %+v %v", st, err)
	}
	holder.Exec(t.Context(), `SELECT pg_advisory_unlock(1213680466)`)
	if st, _ := e.job(100).RunOnce(t.Context()); st.Skipped || st.CheckResults != 5 {
		t.Fatalf("after release it runs: %+v", st)
	}
	// The job must release its own lock so the next pass is not blocked.
	if st, _ := e.job(100).RunOnce(t.Context()); st.Skipped {
		t.Fatal("lock leaked")
	}
}

func TestExpiredCredentialsAreTidied(t *testing.T) {
	e := newEnv(t)
	var uid uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash) VALUES ('a@example.com','x') RETURNING id`).Scan(&uid)
	e.pool.Exec(t.Context(), `INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1,'\x01', now() - interval '1 hour'), ($1,'\x02', now() + interval '1 day')`, uid)
	e.pool.Exec(t.Context(), `INSERT INTO password_reset_tokens (user_id, token_hash, expires_at, used_at) VALUES
		($1,'\x03', now() - interval '10 days', NULL), ($1,'\x04', now() + interval '1 hour', NULL), ($1,'\x05', now() + interval '1 hour', now() - interval '9 days')`, uid)
	st, _ := e.job(100).RunOnce(t.Context())
	if st.Sessions != 1 || st.Tokens != 2 {
		t.Fatalf("%+v", st)
	}
	if e.count(t, `SELECT count(*) FROM sessions`) != 1 || e.count(t, `SELECT count(*) FROM password_reset_tokens`) != 1 {
		t.Fatal("valid sessions and live tokens must remain")
	}
}

func TestCancelledPassStopsPromptly(t *testing.T) {
	e := newEnv(t)
	m := e.monitor(t, e.org(t, "free", "active"))
	e.results(t, m, 300, 30)
	ctx, cancel := t.Context(), func() {}
	_ = cancel
	j := retention.New(e.pool, retention.Config{Batch: 10, Pause: 20 * time.Millisecond}, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c2, stop := contextWithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	start := time.Now()
	j.RunOnce(c2)
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancellation not honoured")
	}
	if e.resultsOf(t, m) == 0 {
		t.Fatal("the pass should have been interrupted before finishing")
	}
}

func TestOldBillingEventsAreTrimmedByStatus(t *testing.T) {
	e := newEnv(t)
	ins := func(id, status, age string) {
		if _, err := e.pool.Exec(t.Context(), `INSERT INTO billing_webhook_events (provider_event_id, event_type, event_created_at, status, payload, received_at)
			VALUES ($1, 'customer.subscription.updated', now(), $2, '{}', now() - $3::interval)`, id, status, age); err != nil {
			t.Fatal(err)
		}
	}
	ins("old_processed", "processed", "100 days")
	ins("old_ignored", "ignored", "100 days")
	ins("recent_processed", "processed", "10 days")
	ins("old_failed", "failed", "100 days") // failed events are kept longer for investigation
	ins("very_old_failed", "failed", "200 days")
	ins("old_pending", "received", "100 days") // unprocessed events are never trimmed
	st, err := e.job(100).RunOnce(t.Context())
	if err != nil || st.BillingEvents != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	var remaining []string
	rows, _ := e.pool.Query(t.Context(), `SELECT provider_event_id FROM billing_webhook_events ORDER BY provider_event_id`)
	for rows.Next() {
		var id string
		rows.Scan(&id)
		remaining = append(remaining, id)
	}
	rows.Close()
	want := []string{"old_failed", "old_pending", "recent_processed"}
	if len(remaining) != 3 || remaining[0] != want[0] || remaining[1] != want[1] || remaining[2] != want[2] {
		t.Fatalf("remaining %v, want %v", remaining, want)
	}
}
