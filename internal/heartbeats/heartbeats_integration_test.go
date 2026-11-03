package heartbeats_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/heartbeats"
	"hawkeye/internal/incidents"
	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

type env struct {
	t        *testing.T
	pool     *pgxpool.Pool
	svc      *heartbeats.Service
	sweeper  *heartbeats.Sweeper
	monitors *monitors.Service
	org      uuid.UUID
}

func newEnv(t *testing.T) (*env, func(q string, args ...any)) {
	t.Helper()
	pool := testutil.NewPool(t)
	store := checks.NewStore(pool, heartbeats.Thresholds, incidents.Engine{})
	e := &env{t: t, pool: pool, svc: heartbeats.NewService(pool, store), sweeper: heartbeats.NewSweeper(pool, store, heartbeats.SweeperConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		monitors: monitors.NewService(pool, plans.NewService("http://app.test"))}
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&e.org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination, verified_at) VALUES ($1, 'email', 'ops@example.com', now())`, e.org); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	return e, exec
}

func intPtr(v int) *int { return &v }

// heartbeat creates a heartbeat monitor expected hourly with ten minutes' grace (the Free plan allows periods from five minutes).
func (e *env) heartbeat(name string) monitors.Monitor {
	e.t.Helper()
	m, err := e.monitors.Create(e.t.Context(), e.org, monitors.CreateInput{Type: monitors.TypeHeartbeat, Name: name,
		CheckIntervalSeconds: intPtr(3600), GraceSeconds: intPtr(600)})
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) get(id uuid.UUID) monitors.Monitor {
	e.t.Helper()
	m, err := e.monitors.Get(e.t.Context(), e.org, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) ping(token string, p heartbeats.Ping) bool {
	e.t.Helper()
	if p.Method == "" {
		p.Method, p.SourceIP = "GET", "203.0.113.7"
	}
	counted, err := e.svc.Ping(e.t.Context(), token, p)
	if err != nil {
		e.t.Fatal(err)
	}
	return counted
}

func (e *env) sweep() heartbeats.SweepStats {
	e.t.Helper()
	st, err := e.sweeper.Tick(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func TestHeartbeatLifecycle(t *testing.T) {
	e, exec := newEnv(t)
	m := e.heartbeat("Nightly backup")
	if m.Type != monitors.TypeHeartbeat || m.Heartbeat == nil || len(m.Heartbeat.PingToken) != 22 || m.Heartbeat.GraceSeconds != 600 ||
		m.Heartbeat.DueBy != nil || m.CurrentStatus != checks.StatusUnknown || m.URL != "" {
		t.Fatalf("new heartbeat: %+v %+v", m, m.Heartbeat)
	}
	token := m.Heartbeat.PingToken
	exec(`UPDATE ping_intake SET gap_ended_at = now() - interval '1 day'`)
	if err := heartbeats.MarkIntake(t.Context(), e.pool); err != nil {
		t.Fatal(err)
	}

	// Waiting for the first ping: never missed, however long it takes.
	exec(`UPDATE monitors SET created_at = now() - interval '30 days' WHERE id = $1`, m.ID)
	if st := e.sweep(); st.Missed != 0 {
		t.Fatalf("a heartbeat waiting for its first ping was missed: %+v", st)
	}

	if !e.ping(token, heartbeats.Ping{}) {
		t.Fatal("ping not counted")
	}
	got := e.get(m.ID)
	if got.CurrentStatus != checks.StatusUp || got.Heartbeat.LastPingAt == nil || got.Heartbeat.DueBy == nil {
		t.Fatalf("after the first ping: %+v %+v", got, got.Heartbeat)
	}
	if d := got.Heartbeat.DueBy.Sub(*got.Heartbeat.LastPingAt); d != 70*time.Minute {
		t.Fatalf("due a period plus grace after the ping, got %v", d)
	}

	// The job stops: once the deadline passes, the sweep records a missed run, opens an incident and queues the alert.
	exec(`UPDATE monitors SET last_ping_at = now() - interval '2 hours', ping_deadline = now() - interval '50 minutes' WHERE id = $1`, m.ID)
	exec(`UPDATE check_results SET checked_at = now() - interval '2 hours', finished_at = now() - interval '2 hours' WHERE monitor_id = $1`, m.ID)
	if st := e.sweep(); st.Missed != 1 {
		t.Fatalf("sweep: %+v", st)
	}
	var cause, summary string
	var started, deadline time.Time
	q := e.pool
	if err := q.QueryRow(t.Context(), `SELECT i.cause_category, i.summary, i.started_at FROM incidents i WHERE monitor_id = $1 AND status = 'open'`, m.ID).
		Scan(&cause, &summary, &started); err != nil {
		t.Fatal(err)
	}
	if err := q.QueryRow(t.Context(), `SELECT ping_deadline FROM monitors WHERE id = $1`, m.ID).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if cause != heartbeats.CatMissed || summary[:13] != "No ping since" || time.Since(started) < 49*time.Minute {
		t.Fatalf("incident: %s %q started %v", cause, summary, started)
	}
	// The next missed run is a period after the deadline that was missed.
	if d := time.Until(deadline); d < 9*time.Minute || d > 11*time.Minute {
		t.Fatalf("next deadline in %v", d)
	}
	if n := count(t, e, `SELECT count(*) FROM notification_deliveries WHERE monitor_id = $1 OR incident_id IN (SELECT id FROM incidents WHERE monitor_id = $1)`, m.ID); n != 1 {
		t.Fatalf("outage deliveries: %d", n)
	}
	if st := e.sweep(); st.Missed != 0 {
		t.Fatalf("missed twice in one period: %+v", st)
	}

	// A ping resolves the incident.
	e.ping(token, heartbeats.Ping{})
	if got := e.get(m.ID); got.CurrentStatus != checks.StatusUp {
		t.Fatalf("after recovering: %s", got.CurrentStatus)
	}
	if n := count(t, e, `SELECT count(*) FROM incidents WHERE monitor_id = $1 AND status = 'open'`, m.ID); n != 0 {
		t.Fatal("the incident is still open")
	}

	// The job reports a failure itself: an incident at once, with the exit status.
	e.ping(token, heartbeats.Ping{Fail: true, ExitStatus: intPtr(3), Method: "POST", SourceIP: "203.0.113.7", Body: "disk full\x00", UserAgent: "curl/8\n"})
	if err := q.QueryRow(t.Context(), `SELECT cause_category, summary FROM incidents WHERE monitor_id = $1 AND status = 'open'`, m.ID).Scan(&cause, &summary); err != nil {
		t.Fatal(err)
	}
	if cause != heartbeats.CatJobFailed || summary != "The job exited with status 3" {
		t.Fatalf("failure incident: %s %q", cause, summary)
	}

	// Paused: logged, not counted.
	if _, err := e.monitors.SetEnabled(t.Context(), e.org, m.ID, false); err != nil {
		t.Fatal(err)
	}
	if e.ping(token, heartbeats.Ping{}) {
		t.Fatal("a paused heartbeat counted a ping")
	}
	if got := e.get(m.ID); got.CurrentStatus != checks.StatusDown {
		t.Fatalf("a ping changed a paused heartbeat: %s", got.CurrentStatus)
	}
	// Resuming gives the job a full period and the grace time.
	resumed, err := e.monitors.SetEnabled(t.Context(), e.org, m.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(*resumed.Heartbeat.DueBy); d < 69*time.Minute || d > 71*time.Minute {
		t.Fatalf("due %v after resuming", d)
	}

	pings, err := e.svc.Pings(t.Context(), e.org, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 4 || !pings[0].Ignored || pings[1].Kind != "fail" || *pings[1].ExitStatus != 3 || *pings[1].Body != "disk full" ||
		*pings[1].UserAgent != "curl/8" || pings[3].SourceIP != "203.0.113.7" {
		t.Fatalf("ping log: %+v", pings)
	}
	if _, err := e.svc.Pings(t.Context(), uuid.New(), m.ID, 10); !errors.Is(err, heartbeats.ErrNotFound) {
		t.Fatalf("another organization read the ping log: %v", err)
	}

	for _, bad := range []string{"", "short", "AAAAAAAAAAAAAAAAAAAAAA", token + "x", "AAAAAAAAAAAAAAAAAAAA/."} {
		if _, err := e.svc.Ping(t.Context(), bad, heartbeats.Ping{Method: "GET", SourceIP: "203.0.113.7"}); !errors.Is(err, heartbeats.ErrNotFound) {
			t.Errorf("token %q: %v", bad, err)
		}
	}

	// A new address replaces the old one; deleting the monitor turns its address off.
	rotated, err := e.monitors.RotatePingToken(t.Context(), e.org, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Ping(t.Context(), token, heartbeats.Ping{Method: "GET", SourceIP: "203.0.113.7"}); !errors.Is(err, heartbeats.ErrNotFound) {
		t.Fatalf("the old address still works: %v", err)
	}
	e.ping(rotated.Heartbeat.PingToken, heartbeats.Ping{})
	if err := e.monitors.Delete(t.Context(), e.org, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Ping(t.Context(), rotated.Heartbeat.PingToken, heartbeats.Ping{Method: "GET", SourceIP: "203.0.113.7"}); !errors.Is(err, heartbeats.ErrNotFound) {
		t.Fatalf("a deleted monitor's address still works: %v", err)
	}
}

// HawkEye's own downtime must never be reported as the customer's job having missed a run.
func TestIntakeGapNeverBlamesTheJob(t *testing.T) {
	e, exec := newEnv(t)
	m := e.heartbeat("Queue worker")
	e.ping(m.Heartbeat.PingToken, heartbeats.Ping{})
	exec(`UPDATE monitors SET last_ping_at = now() - interval '2 hours', ping_deadline = now() - interval '50 minutes' WHERE id = $1`, m.ID)

	// The API has not marked the intake for five minutes: it may be down right now, so nothing is decided.
	exec(`UPDATE ping_intake SET seen_at = now() - interval '5 minutes', gap_ended_at = now() - interval '1 day'`)
	if st := e.sweep(); !st.IntakeDown || st.Missed != 0 {
		t.Fatalf("sweep while the intake is down: %+v", st)
	}
	// It comes back. The job's ping may have been lost in the gap, so it gets a full period (and grace) from the end of the gap.
	if err := heartbeats.MarkIntake(t.Context(), e.pool); err != nil {
		t.Fatal(err)
	}
	if st := e.sweep(); st.Missed != 0 || st.Postponed != 1 {
		t.Fatalf("sweep after the gap: %+v", st)
	}
	got := e.get(m.ID)
	if got.CurrentStatus != checks.StatusUp {
		t.Fatalf("status after the gap: %s", got.CurrentStatus)
	}
	if d := time.Until(*got.Heartbeat.DueBy); d < 69*time.Minute || d > 71*time.Minute {
		t.Fatalf("due %v after the gap", d)
	}
	// A gap long before the last ping changes nothing.
	exec(`UPDATE ping_intake SET gap_ended_at = now() - interval '1 day'`)
	exec(`UPDATE monitors SET ping_deadline = now() - interval '1 minute' WHERE id = $1`, m.ID)
	if st := e.sweep(); st.Missed != 1 {
		t.Fatalf("a real miss after an old gap: %+v", st)
	}
}

func TestPingLogKeepsTheLatest(t *testing.T) {
	e, _ := newEnv(t)
	m := e.heartbeat("Busy job")
	for range heartbeats.KeptPings + 5 {
		e.ping(m.Heartbeat.PingToken, heartbeats.Ping{})
	}
	if n := count(t, e, `SELECT count(*) FROM heartbeat_pings WHERE monitor_id = $1`, m.ID); n != heartbeats.KeptPings {
		t.Fatalf("kept %d pings", n)
	}
}

func TestHeartbeatSettings(t *testing.T) {
	e, _ := newEnv(t)
	ctx := t.Context()
	for name, in := range map[string]monitors.CreateInput{
		"a url":           {Type: "heartbeat", Name: "x", URL: "https://example.com"},
		"regions":         {Type: "heartbeat", Name: "x", Regions: []string{"primary"}},
		"headers":         {Type: "heartbeat", Name: "x", Headers: []checks.Header{{Name: "X-A", Value: "b"}}},
		"short grace":     {Type: "heartbeat", Name: "x", GraceSeconds: intPtr(30)},
		"long period":     {Type: "heartbeat", Name: "x", CheckIntervalSeconds: intPtr(31 * 86400)},
		"slow threshold":  {Type: "heartbeat", Name: "x", SlowThresholdMs: intPtr(500)},
		"an unknown type": {Type: "tcp", Name: "x", URL: "https://example.com"},
	} {
		var ve *validation.Error
		if _, err := e.monitors.Create(ctx, e.org, in); !errors.As(err, &ve) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The plan's minimum interval is the shortest period (Free: five minutes).
	var le *plans.LimitError
	if _, err := e.monitors.Create(ctx, e.org, monitors.CreateInput{Type: "heartbeat", Name: "x", CheckIntervalSeconds: intPtr(120)}); !errors.As(err, &le) {
		t.Fatalf("a period under the plan minimum: %v", err)
	}
	// Defaults: daily, with an hour's grace. A weekly job is fine.
	m, err := e.monitors.Create(ctx, e.org, monitors.CreateInput{Type: "heartbeat", Name: "Daily"})
	if err != nil || m.CheckIntervalSeconds != 86400 || m.Heartbeat.GraceSeconds != 3600 {
		t.Fatalf("defaults: %v %+v", err, m)
	}
	if _, err := e.monitors.Create(ctx, e.org, monitors.CreateInput{Type: "heartbeat", Name: "Weekly", CheckIntervalSeconds: intPtr(7 * 86400)}); err != nil {
		t.Fatal(err)
	}
	site, err := e.monitors.Create(ctx, e.org, monitors.CreateInput{Name: "Site", URL: "https://example.com"})
	if err != nil || site.Type != monitors.TypeHTTP || site.Heartbeat != nil {
		t.Fatalf("website monitor: %v %+v", err, site)
	}
	if _, err := e.monitors.Update(ctx, e.org, site.ID, monitors.UpdateInput{GraceSeconds: intPtr(600)}); err == nil {
		t.Fatal("a website monitor took a grace time")
	}

	// Changing the period moves the deadline (from the last ping).
	e.ping(m.Heartbeat.PingToken, heartbeats.Ping{})
	upd, err := e.monitors.Update(ctx, e.org, m.ID, monitors.UpdateInput{CheckIntervalSeconds: intPtr(7200), GraceSeconds: intPtr(300)})
	if err != nil {
		t.Fatal(err)
	}
	if d := upd.Heartbeat.DueBy.Sub(*upd.Heartbeat.LastPingAt); d != 125*time.Minute {
		t.Fatalf("deadline after the edit: %v", d)
	}

	// Bulk "set interval" is for website checks: it leaves a heartbeat's period alone.
	if _, err := e.monitors.Bulk(ctx, e.org, monitors.BulkInput{MonitorIDs: []uuid.UUID{m.ID, site.ID}, Action: monitors.BulkSetInterval,
		CheckIntervalSeconds: intPtr(600)}); err != nil {
		t.Fatal(err)
	}
	if e.get(m.ID).CheckIntervalSeconds != 7200 || e.get(site.ID).CheckIntervalSeconds != 600 {
		t.Fatal("bulk set_interval changed a heartbeat")
	}
	// The scheduler never picks a heartbeat up.
	if n := count(t, e, `SELECT count(*) FROM monitors WHERE kind = 'heartbeat' AND next_check_at <> 'infinity'`); n != 0 {
		t.Fatalf("%d heartbeats are due for a check", n)
	}
}

func count(t *testing.T, e *env, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
