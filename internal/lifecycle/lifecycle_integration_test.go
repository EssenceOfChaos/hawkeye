package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
	"hawkeye/internal/lifecycle"
	"hawkeye/internal/metrics"
	"hawkeye/internal/testutil"
)

const (
	from    = "HawkEye <customer_support@hawkeye.test>"
	replyTo = "help@hawkeye.test"
	base    = "https://app.hawkeye.test"
)

type env struct {
	pool *pgxpool.Pool
	mail *email.Memory
}

func newEnv(t *testing.T) *env { return &env{pool: testutil.NewPool(t), mail: &email.Memory{}} }

func (e *env) job(s email.Sender) *lifecycle.Job {
	return lifecycle.NewJob(e.pool, s, lifecycle.Config{From: from, ReplyTo: replyTo, AppBaseURL: base, Address: "1 Main St"},
		metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// user adds an account whose address was verified `age` ago (zero age = unverified) and returns its id and its organization.
func (e *env) user(t *testing.T, addr string, verifiedAgo time.Duration) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var uid, org uuid.UUID
	var verified any
	if verifiedAgo > 0 {
		verified = time.Now().Add(-verifiedAgo)
	}
	if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, email_verified_at) VALUES ($1, 'x', $2) RETURNING id`, addr, verified).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, org, uid); err != nil {
		t.Fatal(err)
	}
	return uid, org
}

func (e *env) monitor(t *testing.T, org uuid.UUID, deleted bool) {
	t.Helper()
	q := `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'm', 'https://example.com', 300)`
	if deleted {
		q = `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, deleted_at) VALUES ($1, 'm', 'https://example.com', 300, now())`
	}
	if _, err := e.pool.Exec(context.Background(), q, org); err != nil {
		t.Fatal(err)
	}
}

func (e *env) sentKinds(t *testing.T, uid uuid.UUID) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT kind FROM lifecycle_emails WHERE user_id = $1 ORDER BY sent_at, kind`, uid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		out = append(out, k)
	}
	return out
}

func (e *env) run(t *testing.T, s email.Sender) lifecycle.Stats {
	t.Helper()
	st, err := e.job(s).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

const h = time.Hour

func TestFirstNudgeGoesOutOnceWithTheRightSenderAndOptOut(t *testing.T) {
	e := newEnv(t)
	uid, _ := e.user(t, "new@example.com", 25*h)

	if st := e.run(t, e.mail); st.Sent != 1 {
		t.Fatalf("sent = %d", st.Sent)
	}
	msgs := e.mail.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	m := msgs[0]
	if m.To != "new@example.com" || m.From != from || m.ReplyTo != replyTo || m.Subject != lifecycle.Rules[0].Subject {
		t.Fatalf("envelope: %+v", m)
	}
	var token string
	e.pool.QueryRow(context.Background(), `SELECT unsubscribe_token::text FROM users WHERE id = $1`, uid).Scan(&token)
	if m.Unsubscribe != base+"/api/v1/lifecycle/unsubscribe?token="+token {
		t.Fatalf("one-click URL = %q", m.Unsubscribe)
	}
	for _, want := range []string{base + "/unsubscribe?token=" + token, "utm_campaign=no_monitor_1d", "1 Main St", "Add your first website"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	for _, want := range []string{base + "/unsubscribe?token=" + token, "1 Main St", "Stop these emails"} {
		if !strings.Contains(m.HTML, want) {
			t.Errorf("html missing %q", want)
		}
	}
	if got := e.sentKinds(t, uid); len(got) != 1 || got[0] != lifecycle.KindNoMonitor1d {
		t.Fatalf("recorded = %v", got)
	}
	// Nothing more on the next pass.
	if st := e.run(t, e.mail); st.Sent != 0 || len(e.mail.Messages()) != 1 {
		t.Fatalf("a second pass sent again: %+v", st)
	}
}

func TestOnlyPeopleWhoShouldGetAnEmailDo(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, e *env) // makes one account that must NOT be mailed
	}{
		{"has a monitor", func(t *testing.T, e *env) { _, o := e.user(t, "a@example.com", 30*h); e.monitor(t, o, false) }},
		{"had a monitor and deleted it", func(t *testing.T, e *env) { _, o := e.user(t, "a@example.com", 30*h); e.monitor(t, o, true) }},
		{"belongs to an organization that has one", func(t *testing.T, e *env) {
			uid, _ := e.user(t, "a@example.com", 30*h)
			_, other := e.user(t, "owner@example.com", 0)
			e.monitor(t, other, false)
			e.pool.Exec(context.Background(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'member')`, other, uid)
		}},
		{"address not verified", func(t *testing.T, e *env) { e.user(t, "a@example.com", 0) }},
		{"verified less than a day ago", func(t *testing.T, e *env) { e.user(t, "a@example.com", 23*h) }},
		{"verified too long ago", func(t *testing.T, e *env) { e.user(t, "a@example.com", 11*24*h) }},
		{"between windows", func(t *testing.T, e *env) { e.user(t, "a@example.com", 6*24*h+12*h) }},
		{"unsubscribed", func(t *testing.T, e *env) {
			uid, _ := e.user(t, "a@example.com", 30*h)
			e.pool.Exec(context.Background(), `UPDATE users SET lifecycle_opt_out_at = now() WHERE id = $1`, uid)
		}},
		{"account disabled", func(t *testing.T, e *env) {
			uid, _ := e.user(t, "a@example.com", 30*h)
			e.pool.Exec(context.Background(), `UPDATE users SET disabled_at = now() WHERE id = $1`, uid)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.setup(t, e)
			if st := e.run(t, e.mail); st.Sent != 0 || len(e.mail.Messages()) != 0 {
				t.Fatalf("mailed someone who should not be: %+v %v", st, e.mail.Messages())
			}
		})
	}
}

func TestEachWindowSendsItsOwnEmailAndPeopleAreNotMailedTooCloseTogether(t *testing.T) {
	e := newEnv(t)
	a, _ := e.user(t, "day3@example.com", 80*h)    // in the 3-day window
	b, _ := e.user(t, "day7@example.com", 8*24*h)  // in the 7-day window
	c, _ := e.user(t, "recent@example.com", 80*h)  // in the 3-day window, but mailed yesterday
	d, _ := e.user(t, "settled@example.com", 80*h) // in the 3-day window, last mailed three days ago
	e.pool.Exec(context.Background(), `INSERT INTO lifecycle_emails (user_id, kind, sent_at) VALUES ($1, 'no_monitor_1d', now() - interval '1 day')`, c)
	e.pool.Exec(context.Background(), `INSERT INTO lifecycle_emails (user_id, kind, sent_at) VALUES ($1, 'no_monitor_1d', now() - interval '3 days')`, d)

	if st := e.run(t, e.mail); st.Sent != 3 {
		t.Fatalf("sent = %d, want 3", st.Sent)
	}
	if got := e.sentKinds(t, a); len(got) != 1 || got[0] != lifecycle.KindNoMonitor3d {
		t.Fatalf("a: %v", got)
	}
	if got := e.sentKinds(t, b); len(got) != 1 || got[0] != lifecycle.KindNoMonitor7d {
		t.Fatalf("b: %v", got)
	}
	if got := e.sentKinds(t, c); len(got) != 1 || got[0] != lifecycle.KindNoMonitor1d {
		t.Fatalf("c was mailed again too soon: %v", got)
	}
	if got := e.sentKinds(t, d); len(got) != 2 {
		t.Fatalf("d: %v", got)
	}
}

func TestStopsTheMomentAMonitorExists(t *testing.T) {
	e := newEnv(t)
	_, org := e.user(t, "new@example.com", 25*h)
	e.monitor(t, org, false) // added between signup and the pass
	if st := e.run(t, e.mail); st.Sent != 0 {
		t.Fatalf("mailed someone who already started: %+v", st)
	}
}

type failing struct{ err error }

func (f failing) Send(context.Context, email.Message) error { return f.err }

func TestATemporaryFailureReleasesTheClaimSoItIsRetried(t *testing.T) {
	e := newEnv(t)
	uid, _ := e.user(t, "new@example.com", 25*h)

	if _, err := e.job(failing{errors.New("connection refused")}).RunOnce(context.Background()); err == nil {
		t.Fatal("a failed send must be reported")
	}
	if got := e.sentKinds(t, uid); len(got) != 0 {
		t.Fatalf("claim kept after a temporary failure: %v", got)
	}
	if st := e.run(t, e.mail); st.Sent != 1 {
		t.Fatalf("not retried: %+v", st)
	}
}

func TestAPermanentRefusalIsNotRetried(t *testing.T) {
	e := newEnv(t)
	uid, _ := e.user(t, "bad@example.com", 25*h)
	st, err := e.job(failing{fmt.Errorf("%w: mailbox does not exist", email.ErrPermanent)}).RunOnce(context.Background())
	if err != nil || st.Sent != 0 {
		t.Fatalf("%v %+v", err, st)
	}
	if got := e.sentKinds(t, uid); len(got) != 1 {
		t.Fatalf("claim must stay so the address is not tried forever: %v", got)
	}
	if st := e.run(t, e.mail); st.Sent != 0 {
		t.Fatalf("retried a refused address: %+v", st)
	}
}

type slowMemory struct {
	*email.Memory
	delay time.Duration
}

func (s slowMemory) Send(ctx context.Context, m email.Message) error {
	time.Sleep(s.delay)
	return s.Memory.Send(ctx, m)
}

func TestSeveralWorkersNeverSendTheSameEmailTwice(t *testing.T) {
	e := newEnv(t)
	for i := range 10 {
		e.user(t, fmt.Sprintf("u%d@example.com", i), 25*h)
	}
	s := slowMemory{e.mail, 5 * time.Millisecond}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); e.job(s).RunOnce(context.Background()) }()
	}
	wg.Wait()
	seen := map[string]int{}
	for _, m := range e.mail.Messages() {
		seen[m.To]++
	}
	if len(seen) != 10 {
		t.Fatalf("%d people mailed, want 10", len(seen))
	}
	for to, n := range seen {
		if n != 1 {
			t.Errorf("%s got %d copies", to, n)
		}
	}
}

func TestUnsubscribeStopsTheEmailsAndTouchesNothingElse(t *testing.T) {
	e := newEnv(t)
	uid, _ := e.user(t, "new@example.com", 25*h)
	var token string
	e.pool.QueryRow(context.Background(), `SELECT unsubscribe_token::text FROM users WHERE id = $1`, uid).Scan(&token)
	svc := lifecycle.NewService(e.pool)

	if err := svc.Unsubscribe(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	var first time.Time
	e.pool.QueryRow(context.Background(), `SELECT lifecycle_opt_out_at FROM users WHERE id = $1`, uid).Scan(&first)
	if err := svc.Unsubscribe(context.Background(), token); err != nil {
		t.Fatalf("a second click must also succeed: %v", err)
	}
	var again time.Time
	var verified, disabled *time.Time
	e.pool.QueryRow(context.Background(), `SELECT lifecycle_opt_out_at, email_verified_at, disabled_at FROM users WHERE id = $1`, uid).Scan(&again, &verified, &disabled)
	if !again.Equal(first) || verified == nil || disabled != nil {
		t.Fatalf("opt-out moved or touched other fields: %v %v %v %v", first, again, verified, disabled)
	}
	if st := e.run(t, e.mail); st.Sent != 0 {
		t.Fatalf("mailed someone who unsubscribed: %+v", st)
	}
	for _, bad := range []string{"", "nope", uuid.NewString()} {
		if err := svc.Unsubscribe(context.Background(), bad); !errors.Is(err, lifecycle.ErrInvalidToken) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
}

func TestDeletingAnAccountRemovesItsRecords(t *testing.T) {
	e := newEnv(t)
	uid, _ := e.user(t, "new@example.com", 25*h)
	e.run(t, e.mail)
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	var n int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM lifecycle_emails`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d lifecycle rows left behind", n)
	}
}
