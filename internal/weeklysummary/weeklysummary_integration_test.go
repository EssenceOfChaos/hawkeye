package weeklysummary_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
	"hawkeye/internal/lifecycle"
	"hawkeye/internal/testutil"
	"hawkeye/internal/weeklysummary"
)

func TestLastWeekIsMondayToMondayUTC(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-02T08:00:00Z": "2026-10-26", // Monday morning: the week that just ended
		"2026-11-01T23:00:00Z": "2026-10-19", // Sunday night: the week before (this one has not ended)
		"2026-11-04T12:00:00Z": "2026-10-26",
	} {
		now, _ := time.Parse(time.RFC3339, in)
		from, to := weeklysummary.LastWeek(now)
		if from.Format("2006-01-02") != want || to.Sub(from) != 7*24*time.Hour || from.Weekday() != time.Monday {
			t.Errorf("%s: %s – %s", in, from, to)
		}
	}
}

type env struct {
	pool       *pgxpool.Pool
	mail       *email.Memory
	start, end time.Time
	org        uuid.UUID
	clock      time.Time
	monA, monB uuid.UUID
}

func (e *env) user(t *testing.T, addr, role string, verified, optedOut bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash, email_verified_at, weekly_summary_opt_out_at)
		VALUES ($1, 'x', CASE WHEN $2 THEN now() END, CASE WHEN $3 THEN now() END) RETURNING id`, addr, verified, optedOut).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if role != "" {
		e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, $3)`, e.org, id, role)
	}
	return id
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{pool: testutil.NewPool(t), mail: &email.Memory{}}
	e.start, e.end = weeklysummary.LastWeek(time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC))
	e.clock = e.end.Add(8 * time.Hour) // Monday 08:00 UTC
	ctx := t.Context()
	e.pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('Acme Agency') RETURNING id`).Scan(&e.org)
	e.pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, ssl_expires_at)
		VALUES ($1, 'Shop', 'https://shop.example.com', 60, $2) RETURNING id`, e.org, e.clock.Add(12*24*time.Hour)).Scan(&e.monA)
	e.pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'Blog', 'https://blog.example.com', 60) RETURNING id`, e.org).Scan(&e.monB)
	// Shop: 100 checks, 2 failed, slow; Blog: 100 good, quick. One incident on Shop lasting 30 minutes. A check before the week does not count.
	e.pool.Exec(ctx, `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, response_time_ms)
		SELECT $1, $2::timestamptz + g * interval '1 hour', $2::timestamptz + g * interval '1 hour', g > 2, 1800 FROM generate_series(1, 100) g`, e.monA, e.start)
	e.pool.Exec(ctx, `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, response_time_ms)
		SELECT $1, $2::timestamptz + g * interval '1 hour', $2::timestamptz + g * interval '1 hour', true, 200 FROM generate_series(1, 100) g`, e.monB, e.start)
	e.pool.Exec(ctx, `INSERT INTO check_results (monitor_id, checked_at, finished_at, success) VALUES ($1, $2, $2, false)`, e.monB, e.start.Add(-time.Hour))
	e.pool.Exec(ctx, `INSERT INTO incidents (monitor_id, started_at, resolved_at, status) VALUES ($1, $2, $3, 'resolved')`,
		e.monA, e.start.Add(26*time.Hour), e.start.Add(26*time.Hour+30*time.Minute))
	e.pool.Exec(ctx, `INSERT INTO domains (organization_id, name, status, expires_at) VALUES ($1, 'example.com', 'known', $2)`, e.org, e.clock.Add(20*24*time.Hour))
	return e
}

func (e *env) job(mail email.Sender) *weeklysummary.Job {
	return weeklysummary.NewJob(e.pool, mail, weeklysummary.Config{From: "news@hawkeye.test", AppBaseURL: "https://app.test"}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil))).WithClock(func() time.Time { return e.clock })
}

func (e *env) to(addr string) []email.Message {
	var out []email.Message
	for _, m := range e.mail.Messages() {
		if m.To == addr {
			out = append(out, m)
		}
	}
	return out
}

func TestTheSummaryGoesOnceToEachMemberWhoWantsIt(t *testing.T) {
	e := newEnv(t)
	e.user(t, "owner@example.com", "owner", true, false)
	e.user(t, "member@example.com", "member", true, false)
	e.user(t, "quiet@example.com", "member", true, true)        // turned it off
	e.user(t, "unverified@example.com", "member", false, false) // never mailed
	// Someone whose only organization has no monitors gets nothing.
	var empty uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('Empty') RETURNING id`).Scan(&empty)
	lonely := e.user(t, "lonely@example.com", "", true, false)
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, empty, lonely)

	st, err := e.job(e.mail).RunOnce(t.Context())
	if err != nil || st.Sent != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	for _, addr := range []string{"quiet@example.com", "unverified@example.com", "lonely@example.com"} {
		if len(e.to(addr)) != 0 {
			t.Errorf("%s was mailed", addr)
		}
	}
	m := e.to("member@example.com")[0]
	if m.Subject != "Your week on HawkEye: 99.00% uptime, 1 incident" || m.From != "news@hawkeye.test" {
		t.Fatalf("%q from %q", m.Subject, m.From)
	}
	for _, want := range []string{
		"Acme Agency · 26 Oct – 1 Nov 2026",
		"There was 1 incident, with 30m of downtime in total.",
		"Shop: 98.00% uptime · 1 incident", // least available first
		"Blog: 100% uptime",
		"Slowest on average: Shop (1,800 ms), Blog (200 ms)",
		"SSL certificate: Shop expires in 12 days (14 Nov)",
		"Domain: example.com expires in 20 days (22 Nov)",
		"Account → Email preferences",
		"/unsubscribe?token=",
		"&list=weekly_summary",
	} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("missing %q in:\n%s", want, m.Text)
		}
	}
	if strings.Index(m.Text, "Shop:") > strings.Index(m.Text, "Blog:") || strings.Index(m.Text, "Blog:") > strings.Index(m.Text, "Slowest on average") {
		t.Error("the least available site comes first, and the sites come before the rest")
	}
	if !strings.Contains(m.Unsubscribe, "list=weekly_summary") {
		t.Fatalf("mail clients' own unsubscribe button stops only the summary: %q", m.Unsubscribe)
	}

	if st, _ := e.job(e.mail).RunOnce(t.Context()); st.Sent != 0 {
		t.Fatalf("each person gets each week's summary once: %+v", st)
	}
}

func TestTheSendingWindow(t *testing.T) {
	e := newEnv(t)
	e.user(t, "owner@example.com", "owner", true, false)
	for _, at := range []time.Duration{6 * time.Hour, 3*24*time.Hour + time.Minute} {
		e.clock = e.end.Add(at)
		if st, _ := e.job(e.mail).RunOnce(t.Context()); st.Sent != 0 {
			t.Errorf("%v after the week ended: sent %d", at, st.Sent)
		}
	}
	e.clock = e.end.Add(2*24*time.Hour + 20*time.Hour) // Wednesday evening: a worker that was down on Monday catches up
	if st, _ := e.job(e.mail).RunOnce(t.Context()); st.Sent != 1 {
		t.Fatalf("%+v", st)
	}
}

type flaky struct{ err error }

func (f flaky) Send(context.Context, email.Message) error { return f.err }

func TestATemporaryFailureIsRetriedAndARefusalIsNot(t *testing.T) {
	e := newEnv(t)
	e.user(t, "owner@example.com", "owner", true, false)
	if _, err := e.job(flaky{errors.New("connection reset")}).RunOnce(t.Context()); err == nil {
		t.Fatal("a temporary failure ends the pass with an error")
	}
	if st, _ := e.job(e.mail).RunOnce(t.Context()); st.Sent != 1 {
		t.Fatalf("and the next pass sends it: %+v", st)
	}

	e2 := newEnv(t)
	e2.user(t, "gone@example.com", "owner", true, false)
	if _, err := e2.job(flaky{email.ErrPermanent}).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if st, _ := e2.job(e2.mail).RunOnce(t.Context()); st.Sent != 0 {
		t.Fatalf("a refused address is not retried: %+v", st)
	}
}

func TestUnsubscribingAndPreferences(t *testing.T) {
	e := newEnv(t)
	id := e.user(t, "owner@example.com", "owner", true, false)
	var token string
	e.pool.QueryRow(t.Context(), `SELECT unsubscribe_token::text FROM users WHERE id = $1`, id).Scan(&token)
	svc := lifecycle.NewService(e.pool)

	if p, _ := svc.Preferences(t.Context(), id); !p.WeeklySummary || !p.Onboarding {
		t.Fatalf("both are on by default: %+v", p)
	}
	if err := svc.Unsubscribe(t.Context(), token, lifecycle.ListWeeklySummary); err != nil {
		t.Fatal(err)
	}
	if p, _ := svc.Preferences(t.Context(), id); p.WeeklySummary || !p.Onboarding {
		t.Fatalf("the summary's link stops only the summary: %+v", p)
	}
	if st, _ := e.job(e.mail).RunOnce(t.Context()); st.Sent != 0 {
		t.Fatal("no summary after unsubscribing")
	}
	if err := svc.Unsubscribe(t.Context(), token, "everything"); !errors.Is(err, lifecycle.ErrInvalidToken) {
		t.Fatalf("an unknown list is refused: %v", err)
	}
	on := true
	if p, _ := svc.SetPreferences(t.Context(), id, &on, nil); !p.WeeklySummary {
		t.Fatalf("turned back on in the account: %+v", p)
	}
	if st, _ := e.job(e.mail).RunOnce(t.Context()); st.Sent != 1 {
		t.Fatal("and it arrives again")
	}
}

func TestAQuietWeekAndOneWithTooFewChecks(t *testing.T) {
	e := newEnv(t)
	e.pool.Exec(t.Context(), `DELETE FROM incidents`)
	e.pool.Exec(t.Context(), `UPDATE check_results SET success = true`)
	s, err := weeklysummary.Build(t.Context(), e.pool, e.org, "Acme Agency", e.start, e.end, e.clock)
	if err != nil {
		t.Fatal(err)
	}
	if s.Subject() != "Your week on HawkEye: 100% uptime, no incidents" || !strings.Contains(s.Content("https://app.test", e.clock).Paragraphs[0], "A quiet week") {
		t.Fatalf("%q %+v", s.Subject(), s.Content("https://app.test", e.clock).Paragraphs)
	}
	e.pool.Exec(t.Context(), `DELETE FROM check_results`)
	s, _ = weeklysummary.Build(t.Context(), e.pool, e.org, "Acme Agency", e.start, e.end, e.clock)
	if s.Subject() != "Your week on HawkEye" || s.Content("https://app.test", e.clock).Facts[0].Value != "No checks this week" {
		t.Fatalf("%q %+v", s.Subject(), s.Content("https://app.test", e.clock).Facts)
	}
}
