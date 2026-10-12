package incidents_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidents"
	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
)

type env struct {
	pool    *pgxpool.Pool
	store   *checks.Store
	org     uuid.UUID
	monitor uuid.UUID
	clock   time.Time
}

func newEnv(t *testing.T, conns int32) *env {
	t.Helper()
	pool := testutil.NewPoolSize(t, conns)
	e := &env{pool: pool, store: checks.NewStore(pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{}),
		clock: time.Now().UTC().Add(-time.Hour).Truncate(time.Second)}
	pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&e.org)
	pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code='pro'`, e.org)
	e.monitor = e.newMonitor(t)
	return e
}

func (e *env) newMonitor(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds)
		VALUES ($1, 'Site', 'https://example.com', 60) RETURNING id`, e.org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// recipient adds an alert destination to the organization.
func (e *env) recipient(t *testing.T, addr string, enabled, verified bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination, enabled, verified_at)
		VALUES ($1, 'email', $2, $3, CASE WHEN $4 THEN now() END) RETURNING id`, e.org, addr, enabled, verified).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// result records one check one minute after the previous one.
func (e *env) result(t *testing.T, monitor uuid.UUID, ok bool) checks.Outcome {
	t.Helper()
	e.clock = e.clock.Add(time.Minute)
	r := checks.Result{CheckedAt: e.clock, FinishedAt: e.clock.Add(200 * time.Millisecond), Success: ok}
	if !ok {
		r.ErrorCategory, r.ErrorSummary = checks.CatHTTPStatus, "Received HTTP 503"
		code := 503
		r.StatusCode = &code
	}
	o, err := e.store.Record(t.Context(), monitor, r)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Phase 5 acceptance: a confirmed outage generates one incident and one logical notification per recipient.
func TestConfirmedOutageCreatesOneIncidentAndOneNotificationPerRecipient(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.recipient(t, "b@example.com", true, true)
	e.result(t, e.monitor, true)

	first := e.clock.Add(time.Minute) // time of the first failed check
	e.result(t, e.monitor, false)
	if n := e.count(t, `SELECT count(*) FROM incidents`); n != 0 {
		t.Fatalf("a single failure must not open an incident (have %d)", n)
	}
	if o := e.result(t, e.monitor, false); o.Transition != checks.WentDown {
		t.Fatalf("second failure should confirm: %+v", o)
	}

	var started time.Time
	var cause, summary, status string
	e.pool.QueryRow(t.Context(), `SELECT started_at, cause_category, summary, status FROM incidents`).Scan(&started, &cause, &summary, &status)
	if !started.Equal(first) || cause != "http_status" || summary != "Received HTTP 503" || status != "open" {
		t.Fatalf("started=%v (want %v) cause=%s summary=%s status=%s", started, first, cause, summary, status)
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type='outage' AND status='pending'`); n != 2 {
		t.Fatalf("want one outage notification per recipient, got %d", n)
	}

	// Continued failures change nothing.
	for range 3 {
		e.result(t, e.monitor, false)
	}
	if e.count(t, `SELECT count(*) FROM incidents`) != 1 || e.count(t, `SELECT count(*) FROM notification_deliveries`) != 2 {
		t.Fatal("ongoing outage must not create further incidents or notifications")
	}
}

func TestRecoveryResolvesIncidentAndNotifiesOnce(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)

	e.result(t, e.monitor, true)
	if e.count(t, `SELECT count(*) FROM incidents WHERE status='open'`) != 1 {
		t.Fatal("one success must not close the incident")
	}
	firstSuccess := e.clock
	if o := e.result(t, e.monitor, true); o.Transition != checks.Recovered {
		t.Fatalf("%+v", o)
	}

	var resolved time.Time
	var started time.Time
	e.pool.QueryRow(t.Context(), `SELECT started_at, resolved_at FROM incidents WHERE status='resolved'`).Scan(&started, &resolved)
	if !resolved.Equal(firstSuccess) || resolved.Before(started) {
		t.Fatalf("resolved_at=%v want first success %v", resolved, firstSuccess)
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type='recovery'`); n != 1 {
		t.Fatalf("recovery notifications = %d", n)
	}
	// A further healthy check does not notify again.
	e.result(t, e.monitor, true)
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries`); n != 2 {
		t.Fatalf("deliveries = %d, want outage+recovery only", n)
	}

	// A later outage is a new incident.
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)
	if e.count(t, `SELECT count(*) FROM incidents`) != 2 {
		t.Fatal("a second outage must be a separate incident")
	}
}

func TestOnlyEnabledVerifiedRecipientsAreNotified(t *testing.T) {
	e := newEnv(t, 5)
	ok := e.recipient(t, "ok@example.com", true, true)
	e.recipient(t, "off@example.com", false, true)
	e.recipient(t, "unverified@example.com", true, false)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)

	var got uuid.UUID
	if e.count(t, `SELECT count(*) FROM notification_deliveries`) != 1 {
		t.Fatal("exactly one recipient is eligible")
	}
	e.pool.QueryRow(t.Context(), `SELECT notification_setting_id FROM notification_deliveries`).Scan(&got)
	if got != ok {
		t.Fatal("wrong recipient")
	}
}

func TestFlappingCreatesNoIncident(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	for _, ok := range []bool{true, false, true, false, true, false, true} {
		e.result(t, e.monitor, ok)
	}
	if e.count(t, `SELECT count(*) FROM incidents`) != 0 || e.count(t, `SELECT count(*) FROM notification_deliveries`) != 0 {
		t.Fatal("alternating results must never confirm an outage")
	}
}

func TestDatabaseRefusesASecondOpenIncident(t *testing.T) {
	e := newEnv(t, 5)
	ins := `INSERT INTO incidents (monitor_id, started_at) VALUES ($1, now())`
	if _, err := e.pool.Exec(t.Context(), ins, e.monitor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), ins, e.monitor); err == nil {
		t.Fatal("two open incidents for one monitor must violate the unique index")
	}
	// Resolved incidents do not count.
	e.pool.Exec(t.Context(), `UPDATE incidents SET status='resolved', resolved_at=now()`)
	if _, err := e.pool.Exec(t.Context(), ins, e.monitor); err != nil {
		t.Fatalf("a new incident after resolution must be allowed: %v", err)
	}
}

func TestEngineIsIdempotentAndRaceSafe(t *testing.T) {
	e := newEnv(t, 20)
	e.recipient(t, "a@example.com", true, true)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false) // incident opened

	// Re-delivering the same "opened" event, even concurrently, must not duplicate anything.
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := e.pool.Begin(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			if err := (incidents.Engine{}).Opened(t.Context(), tx, e.monitor, 2, "dns", "DNS lookup failed"); err != nil {
				t.Error(err)
			}
			tx.Commit(t.Context())
		}()
	}
	wg.Wait()
	if e.count(t, `SELECT count(*) FROM incidents`) != 1 || e.count(t, `SELECT count(*) FROM notification_deliveries`) != 1 {
		t.Fatalf("incidents=%d deliveries=%d", e.count(t, `SELECT count(*) FROM incidents`), e.count(t, `SELECT count(*) FROM notification_deliveries`))
	}
}

func TestResettingMonitorStateDoesNotLeaveIncidentOpenForever(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)

	// Editing the URL resets health to "unknown" (see monitors.Update); the next healthy check must close the incident.
	e.pool.Exec(t.Context(), `UPDATE monitors SET current_status='unknown', consecutive_failures=0, consecutive_successes=0`)
	e.result(t, e.monitor, true)
	if e.count(t, `SELECT count(*) FROM incidents WHERE status='open'`) != 0 || e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type='recovery'`) != 1 {
		t.Fatal("incident should be resolved with a recovery notification")
	}
}

func TestDeletingMonitorClosesIncidentWithoutNotification(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)

	svc := monitors.NewService(e.pool, plans.NewService(""))
	if err := svc.Delete(t.Context(), e.org, e.monitor); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM incidents WHERE status='open'`) != 0 {
		t.Fatal("incident left open for a deleted monitor")
	}
	if e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type='recovery'`) != 0 {
		t.Fatal("no recovery email for a monitor the user deleted")
	}
}

func TestIncidentAndNotificationsCommitAtomicallyWithTheResult(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	// Break the notification step so Record must fail; the whole transaction (result, status, incident) must roll back.
	e.pool.Exec(t.Context(), `ALTER TABLE notification_deliveries ADD CONSTRAINT always_fails CHECK (false)`)
	e.result(t, e.monitor, false)
	e.clock = e.clock.Add(time.Minute)
	_, err := e.store.Record(t.Context(), e.monitor, checks.Result{CheckedAt: e.clock, FinishedAt: e.clock, ErrorCategory: "other", ErrorSummary: "x"})
	if err == nil {
		t.Fatal("expected failure")
	}
	var status string
	var fails int
	e.pool.QueryRow(t.Context(), `SELECT current_status, consecutive_failures FROM monitors WHERE id=$1`, e.monitor).Scan(&status, &fails)
	if status == "down" || fails != 1 || e.count(t, `SELECT count(*) FROM incidents`) != 0 || e.count(t, `SELECT count(*) FROM check_results`) != 1 {
		t.Fatalf("partial commit: status=%s fails=%d", status, fails)
	}
}

func TestListScopesByOrganizationAndFilters(t *testing.T) {
	e := newEnv(t, 5)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, true)
	e.result(t, e.monitor, true)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false) // second incident, open

	list, total, err := incidents.List(t.Context(), e.pool, e.org, incidents.Filter{Limit: 10})
	if err != nil || total != 2 || len(list) != 2 || list[0].Status != "open" || list[1].Status != "resolved" || list[1].DurationSeconds <= 0 {
		t.Fatalf("%+v total=%d err=%v", list, total, err)
	}
	if l, _, _ := incidents.List(t.Context(), e.pool, e.org, incidents.Filter{Limit: 10, Status: "resolved"}); len(l) != 1 {
		t.Fatalf("status filter: %d", len(l))
	}
	from := list[0].StartedAt
	if l, _, _ := incidents.List(t.Context(), e.pool, e.org, incidents.Filter{Limit: 10, From: &from}); len(l) != 1 {
		t.Fatalf("from filter: %d", len(l))
	}
	if l, _, _ := incidents.List(t.Context(), e.pool, e.org, incidents.Filter{Limit: 1, Offset: 1}); len(l) != 1 || l[0].Status != "resolved" {
		t.Fatalf("pagination: %+v", l)
	}
	var other uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('x') RETURNING id`).Scan(&other)
	if l, total, _ := incidents.List(t.Context(), e.pool, other, incidents.Filter{Limit: 10}); len(l) != 0 || total != 0 {
		t.Fatal("incidents leak across organizations")
	}
}
