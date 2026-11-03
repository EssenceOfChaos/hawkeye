package incidents_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/incidents"
)

// tagged creates a tag and puts the monitor in it.
func (e *env) tagged(t *testing.T, monitor uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var tag uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO tags (organization_id, name) VALUES ($1, $2) RETURNING id`, e.org, name).Scan(&tag); err != nil {
		t.Fatal(err)
	}
	if monitor != uuid.Nil {
		if _, err := e.pool.Exec(t.Context(), `INSERT INTO monitor_tags (monitor_id, tag_id) VALUES ($1, $2)`, monitor, tag); err != nil {
			t.Fatal(err)
		}
	}
	return tag
}

// routeTo limits a destination to monitors carrying the tag.
func (e *env) routeTo(t *testing.T, setting, tag uuid.UUID) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `UPDATE notification_settings SET monitor_scope = 'tags' WHERE id = $1`, setting); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO notification_setting_tags (setting_id, tag_id) VALUES ($1, $2)`, setting, tag); err != nil {
		t.Fatal(err)
	}
}

// memberAddress adds a team member with their own (verified) alert address: what the fallback uses.
func (e *env) memberAddress(t *testing.T, addr string) uuid.UUID {
	t.Helper()
	var user, setting uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, addr).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'member')`, e.org, user); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO notification_settings (organization_id, user_id, channel, destination, verified_at)
		VALUES ($1, $2, 'email', $3, now()) RETURNING id`, e.org, user, addr).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	return setting
}

// down confirms an outage (two failures), with checks ending now, so alert delays are measured against the real clock.
func (e *env) downNow(t *testing.T, monitor uuid.UUID) {
	t.Helper()
	e.clock = time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	e.result(t, monitor, false)
	e.result(t, monitor, false)
}

func (e *env) up(t *testing.T, monitor uuid.UUID) {
	t.Helper()
	e.result(t, monitor, true)
	e.result(t, monitor, true)
}

// recipientsOf lists which destinations got a delivery of the event, by destination address.
func (e *env) recipientsOf(t *testing.T, event string) map[string]string {
	t.Helper()
	rows, err := e.pool.Query(t.Context(), `SELECT s.destination, d.status FROM notification_deliveries d JOIN notification_settings s ON s.id = d.notification_setting_id
		WHERE d.event_type = $1`, event)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (kv [2]string, err error) { return kv, r.Scan(&kv[0], &kv[1]) })
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, kv := range out {
		m[kv[0]] = kv[1]
	}
	return m
}

func TestAlertsGoOnlyToDestinationsRoutedToTheMonitor(t *testing.T) {
	e := newEnv(t, 5)
	acme := e.tagged(t, e.monitor, "Acme")
	beta := e.tagged(t, uuid.Nil, "Beta")
	e.recipient(t, "ops@agency.test", true, true) // all monitors
	e.routeTo(t, e.recipient(t, "acme@client.test", true, true), acme)
	e.routeTo(t, e.recipient(t, "beta@client.test", true, true), beta)

	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)
	got := e.recipientsOf(t, "outage")
	if len(got) != 2 || got["ops@agency.test"] != "pending" || got["acme@client.test"] != "pending" {
		t.Fatalf("outage recipients: %v", got)
	}
	e.up(t, e.monitor)
	if got := e.recipientsOf(t, "recovery"); len(got) != 2 || got["beta@client.test"] != "" {
		t.Fatalf("recovery recipients: %v", got)
	}
}

func TestAnUnroutedAlertFallsBackToTheTeamNeverToOtherClients(t *testing.T) {
	e := newEnv(t, 5)
	acme := e.tagged(t, uuid.Nil, "Acme") // the monitor is NOT in it
	e.routeTo(t, e.recipient(t, "acme@client.test", true, true), acme)
	e.memberAddress(t, "alex@agency.test")

	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)
	if got := e.recipientsOf(t, "outage"); len(got) != 1 || got["alex@agency.test"] != "pending" {
		t.Fatalf("an unrouted outage must reach the team's own address and no client: %v", got)
	}
}

func TestWithNoRouteAndNoTeamAddressNothingIsQueued(t *testing.T) {
	// The Notifications page warns before this happens (coverage); here we only check that no client is told instead.
	e := newEnv(t, 5)
	acme := e.tagged(t, uuid.Nil, "Acme")
	e.routeTo(t, e.recipient(t, "acme@client.test", true, true), acme)
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)
	if got := e.recipientsOf(t, "outage"); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestAlertDelayHoldsTheOutageAndCancelsItIfTheSiteRecoversFirst(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.pool.Exec(t.Context(), `UPDATE monitors SET alert_delay_minutes = 10 WHERE id = $1`, e.monitor)

	e.downNow(t, e.monitor)
	var due, started time.Time
	e.pool.QueryRow(t.Context(), `SELECT d.next_attempt_at, i.started_at FROM notification_deliveries d JOIN incidents i ON i.id = d.incident_id
		WHERE d.event_type = 'outage'`).Scan(&due, &started)
	if !due.Equal(started.Add(10 * time.Minute)) {
		t.Fatalf("outage alert due %v, want 10 minutes after the first failure (%v)", due, started)
	}

	e.up(t, e.monitor)
	var status string
	var suppressed *string
	e.pool.QueryRow(t.Context(), `SELECT status, suppressed FROM notification_deliveries WHERE event_type = 'outage'`).Scan(&status, &suppressed)
	if status != "skipped" || suppressed == nil || *suppressed != "ended_while_held" {
		t.Fatalf("held outage after recovery: %s %v", status, suppressed)
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = 'recovery'`); n != 0 {
		t.Fatalf("nobody heard about the outage, so nobody gets a recovery; got %d", n)
	}
}

func TestAnOutageLongerThanTheDelayIsAnnouncedAsUsual(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.pool.Exec(t.Context(), `UPDATE monitors SET alert_delay_minutes = 10 WHERE id = $1`, e.monitor)
	// The env's clock starts an hour ago: the outage has long outlasted the delay by the time it is confirmed.
	e.result(t, e.monitor, false)
	e.result(t, e.monitor, false)
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = 'outage' AND next_attempt_at <= now()`); n != 1 {
		t.Fatalf("an elapsed delay must not hold the alert; due now: %d", n)
	}
}

func TestALateButUnheldAlertIsNeverCancelled(t *testing.T) {
	// No delay, no quiet hours, and the dispatcher has not sent the outage alert yet (for example it was down) when the site recovers: both alerts go.
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.downNow(t, e.monitor)
	e.up(t, e.monitor)
	if got := e.recipientsOf(t, "outage"); got["ops@example.com"] != "pending" {
		t.Fatalf("outage: %v", got)
	}
	if got := e.recipientsOf(t, "recovery"); got["ops@example.com"] != "pending" {
		t.Fatalf("recovery: %v", got)
	}
}

// quietNow sets quiet hours on the monitor that cover the current moment and end in about an hour.
func (e *env) quietNow(t *testing.T, monitor uuid.UUID) time.Time {
	t.Helper()
	now := time.Now().UTC()
	minute := now.Hour()*60 + now.Minute()
	start, end := (minute+1440-60)%1440, (minute+60)%1440
	if _, err := e.pool.Exec(t.Context(), `UPDATE monitors SET quiet_start = $2, quiet_end = $3, quiet_timezone = 'UTC' WHERE id = $1`, monitor, start, end); err != nil {
		t.Fatal(err)
	}
	y, m, d := now.Date()
	return time.Date(y, m, d, end/60, end%60, 0, 0, time.UTC).AddDate(0, 0, map[bool]int{true: 1, false: 0}[end < minute])
}

func TestQuietHoursHoldAlertsUntilTheyEnd(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	end := e.quietNow(t, e.monitor)

	e.downNow(t, e.monitor)
	var due time.Time
	e.pool.QueryRow(t.Context(), `SELECT next_attempt_at FROM notification_deliveries WHERE event_type = 'outage'`).Scan(&due)
	if !due.Equal(end) {
		t.Fatalf("outage alert due %v, want the end of quiet hours %v", due, end)
	}
	// Still down at the end of the night: it is sent then. Recovering during quiet hours cancels it and sends no recovery.
	e.up(t, e.monitor)
	if got := e.recipientsOf(t, "outage"); got["ops@example.com"] != "skipped" {
		t.Fatalf("outage after recovery in quiet hours: %v", got)
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = 'recovery'`); n != 0 {
		t.Fatalf("recovery sent for an outage nobody heard about: %d", n)
	}
}

func TestARecoveryDuringQuietHoursWaitsForTheMorning(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.downNow(t, e.monitor)
	e.pool.Exec(t.Context(), `UPDATE notification_deliveries SET status = 'sent', sent_at = now() WHERE event_type = 'outage'`) // told before the night
	end := e.quietNow(t, e.monitor)

	e.up(t, e.monitor)
	var due time.Time
	if err := e.pool.QueryRow(t.Context(), `SELECT next_attempt_at FROM notification_deliveries WHERE event_type = 'recovery'`).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if !due.Equal(end) {
		t.Fatalf("recovery due %v, want %v", due, end)
	}
}

func TestQuietHoursHoldSlowAlertsToo(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	end := e.quietNow(t, e.monitor)
	run := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := pgx.BeginFunc(t.Context(), e.pool, fn); err != nil {
			t.Fatal(err)
		}
	}
	run(func(tx pgx.Tx) error {
		return incidents.Engine{}.SlowStarted(t.Context(), tx, e.monitor, 1000, 2500, time.Now())
	})
	var due time.Time
	e.pool.QueryRow(t.Context(), `SELECT next_attempt_at FROM notification_deliveries WHERE event_type = 'slow'`).Scan(&due)
	if !due.Equal(end) {
		t.Fatalf("slow alert due %v, want %v", due, end)
	}
	run(func(tx pgx.Tx) error { return incidents.Engine{}.SlowEnded(t.Context(), tx, e.monitor, time.Now()) })
	if got := e.recipientsOf(t, "slow"); got["ops@example.com"] != "skipped" {
		t.Fatalf("slow alert after recovery: %v", got)
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = 'slow_recovery'`); n != 0 {
		t.Fatalf("back-to-normal sent for a slowdown nobody heard about: %d", n)
	}
}
