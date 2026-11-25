package incidents_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/incidents"
)

// outage records a confirmed outage and its recovery (two failures, two successes), four minutes on the test clock.
func (e *env) outage(t *testing.T, monitor uuid.UUID, recover bool) {
	t.Helper()
	e.result(t, monitor, false)
	e.result(t, monitor, false)
	if recover {
		e.result(t, monitor, true)
		e.result(t, monitor, true)
	}
}

func (e *env) events(t *testing.T, event string) int {
	t.Helper()
	return e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = $1`, event)
}

func sweep(t *testing.T, e *env) int {
	t.Helper()
	n, err := incidents.NewFlapJob(e.pool, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil))).Sweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) episode(t *testing.T) (status string, outages int) {
	t.Helper()
	if err := e.pool.QueryRow(t.Context(), `SELECT status, outages FROM flap_episodes WHERE monitor_id = $1 ORDER BY started_at DESC LIMIT 1`, e.monitor).
		Scan(&status, &outages); err != nil {
		t.Fatal(err)
	}
	return
}

// The roadmap's case: a monitor that goes down and up repeatedly is alerted once and marked flapping, then hears nothing per outage.
func TestAFlappingMonitorGetsOneAlertInsteadOfOnePerOutage(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.clock = time.Now().UTC().Add(-30 * time.Minute)
	e.result(t, e.monitor, true)

	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	if e.events(t, "outage") != 2 || e.events(t, "recovery") != 2 || e.events(t, "flapping") != 0 {
		t.Fatal("the first two outages are alerted as usual")
	}
	e.outage(t, e.monitor, false) // the third within the hour
	if e.events(t, "outage") != 2 || e.events(t, "flapping") != 1 {
		t.Fatalf("the third outage sends one flapping alert instead of an outage alert: outage=%d flapping=%d", e.events(t, "outage"), e.events(t, "flapping"))
	}
	var flapping *time.Time
	e.pool.QueryRow(t.Context(), `SELECT (SELECT started_at FROM flap_episodes WHERE monitor_id = $1 AND status = 'open')`, e.monitor).Scan(&flapping)
	if flapping == nil {
		t.Fatal("the monitor is marked flapping")
	}
	e.result(t, e.monitor, true)
	e.result(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	if e.events(t, "outage") != 2 || e.events(t, "recovery") != 2 || e.events(t, "flapping") != 1 {
		t.Fatalf("no alerts while flapping: outage=%d recovery=%d flapping=%d", e.events(t, "outage"), e.events(t, "recovery"), e.events(t, "flapping"))
	}
	if n := e.count(t, `SELECT count(*) FROM incidents WHERE monitor_id = $1`, e.monitor); n != 5 {
		t.Fatalf("every outage is still recorded as an incident: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM incidents WHERE alerts_paused AND flap_episode_id IS NOT NULL`); n != 3 {
		t.Fatalf("the outages while flapping are marked: %d", n)
	}
	if s, n := e.episode(t); s != "open" || n != 5 {
		t.Fatalf("episode %s with %d outages", s, n)
	}
	// Recent enough: it neither settles nor counts as staying down yet.
	if sweep(t, e) != 0 {
		t.Fatal("the episode must stay open")
	}
}

func TestAFlappingMonitorThatSettlesSaysSoOnce(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.result(t, e.monitor, true)
	for range 3 {
		e.outage(t, e.monitor, true)
	}
	// The test clock is about 45 minutes back, so the last recovery is more than 30 minutes ago.
	if sweep(t, e) != 1 {
		t.Fatal("the episode settles")
	}
	if s, _ := e.episode(t); s != "settled" || e.events(t, "flapping_ended") != 1 {
		t.Fatalf("status %s, ended alerts %d", s, e.events(t, "flapping_ended"))
	}
	if sweep(t, e) != 0 || e.events(t, "flapping_ended") != 1 {
		t.Fatal("it is said once")
	}
}

// The "nobody gets told" case: a flapping monitor that goes down and stays down must be alerted.
func TestAFlappingMonitorThatStaysDownIsAlerted(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.result(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, false) // starts flapping, and stays down
	var inc uuid.UUID
	e.pool.QueryRow(t.Context(), `SELECT id FROM incidents WHERE status = 'open'`).Scan(&inc)
	before := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE incident_id = $1`, inc)
	if before != 0 {
		t.Fatal("the outage that starts the episode is announced by the flapping alert")
	}
	// It began about 45 minutes ago by the test clock: well past the 15 minutes.
	if sweep(t, e) != 1 {
		t.Fatal("the episode ends")
	}
	if s, _ := e.episode(t); s != "stayed_down" {
		t.Fatalf("status %s", s)
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE incident_id = $1 AND event_type = 'outage'`, inc); n != 1 {
		t.Fatalf("the outage is alerted: %d", n)
	}
	if e.events(t, "flapping_ended") != 0 {
		t.Fatal("the outage alert is the message; no 'stopped flapping'")
	}
	e.result(t, e.monitor, true)
	e.result(t, e.monitor, true)
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE incident_id = $1 AND event_type = 'recovery'`, inc); n != 1 {
		t.Fatalf("its recovery is alerted too: %d", n)
	}
}

func TestAShortOutageOfAFlappingMonitorIsNotAlertedEarly(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.clock = time.Now().UTC().Add(-14 * time.Minute)
	e.result(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, false) // down for about a minute, by now
	if sweep(t, e) != 0 || e.events(t, "outage") != 2 {
		t.Fatal("an outage of a flapping monitor waits 15 minutes (or four intervals) before it is alerted")
	}
}

func TestFlapSuppressionOffAlertsEveryOutage(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.pool.Exec(t.Context(), `UPDATE monitors SET flap_suppression = false WHERE id = $1`, e.monitor)
	e.result(t, e.monitor, true)
	for range 4 {
		e.outage(t, e.monitor, true)
	}
	if e.events(t, "outage") != 4 || e.events(t, "recovery") != 4 || e.events(t, "flapping") != 0 {
		t.Fatalf("outage=%d recovery=%d flapping=%d", e.events(t, "outage"), e.events(t, "recovery"), e.events(t, "flapping"))
	}
}

// Turning flap suppression off during an outage it is holding back alerts that outage at once.
func TestTurningFlapSuppressionOffMidOutageAlertsIt(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.clock = time.Now().UTC().Add(-14 * time.Minute)
	e.result(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, false)
	e.pool.Exec(t.Context(), `UPDATE monitors SET flap_suppression = false WHERE id = $1`, e.monitor)
	if sweep(t, e) != 1 {
		t.Fatal("the episode ends")
	}
	if s, _ := e.episode(t); s != "ended" || e.events(t, "outage") != 3 || e.events(t, "flapping_ended") != 0 {
		t.Fatalf("status %s outage=%d ended=%d", s, e.events(t, "outage"), e.events(t, "flapping_ended"))
	}
}

func TestPausingAFlappingMonitorEndsTheEpisode(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.clock = time.Now().UTC().Add(-14 * time.Minute)
	e.result(t, e.monitor, true)
	for range 3 {
		e.outage(t, e.monitor, true)
	}
	e.pool.Exec(t.Context(), `UPDATE monitors SET enabled = false WHERE id = $1`, e.monitor)
	if sweep(t, e) != 1 {
		t.Fatal("the episode ends")
	}
	if s, _ := e.episode(t); s != "ended" || e.events(t, "flapping_ended") != 1 {
		t.Fatalf("status %s ended=%d", s, e.events(t, "flapping_ended"))
	}
}

func TestNoFlapDecisionDuringMaintenance(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.result(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, true)
	e.outage(t, e.monitor, false)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors)
		VALUES ($1, 'Deploy', now() - interval '1 minute', 60, true)`, e.org); err != nil {
		t.Fatal(err)
	}
	if sweep(t, e) != 0 || e.events(t, "outage") != 2 {
		t.Fatal("a maintenance window raises no alerts; the decision waits for it to end")
	}
}

// Monitors are independent: one flapping doesn't quiet another.
func TestFlappingIsPerMonitor(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	other := e.newMonitor(t)
	e.result(t, e.monitor, true)
	e.result(t, other, true)
	for range 3 {
		e.outage(t, e.monitor, true)
	}
	e.outage(t, other, false)
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries d JOIN incidents i ON i.id = d.incident_id
		WHERE i.monitor_id = $1 AND d.event_type = 'outage'`, other); n != 1 {
		t.Fatalf("the other monitor's outage is alerted: %d", n)
	}
}
