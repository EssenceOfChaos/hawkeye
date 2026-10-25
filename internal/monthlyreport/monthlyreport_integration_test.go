package monthlyreport_test

import (
	"bytes"
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

	"hawkeye/internal/branding"
	"hawkeye/internal/email"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monthlyreport"
	"hawkeye/internal/testutil"
)

var (
	sept     = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	oct2     = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC) // the first pass after September is over
	justOver = time.Date(2026, 10, 1, 0, 20, 0, 0, time.UTC)
)

type env struct {
	pool *pgxpool.Pool
	mail *email.Memory
	br   *branding.Service
}

func newEnv(t *testing.T) *env {
	pool := testutil.NewPool(t)
	return &env{pool: pool, mail: &email.Memory{}, br: branding.NewService(pool)}
}

func (e *env) job(s email.Sender, per int, now time.Time) *monthlyreport.Job {
	return monthlyreport.NewJob(e.pool, s, e.br, monthlyreport.Config{AppBaseURL: "https://app.test", PerEmail: per}, metrics.New(),
		slog.New(slog.NewTextHandler(io.Discard, nil))).WithClock(func() time.Time { return now })
}

func (e *env) run(t *testing.T, s email.Sender, per int, now time.Time) monthlyreport.Stats {
	t.Helper()
	st, err := e.job(s, per, now).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// org creates an organization on a plan with the monthly email switched on.
func (e *env) org(t *testing.T, name, plan string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.setPlan(t, id, plan)
	if err := e.br.SetMonthlyEmail(t.Context(), id, true); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) setPlan(t *testing.T, org uuid.UUID, plan string) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = $2
		ON CONFLICT (organization_id) DO UPDATE SET plan_id = EXCLUDED.plan_id`, org, plan); err != nil {
		t.Fatal(err)
	}
}

func (e *env) member(t *testing.T, org uuid.UUID, addr, role string, verified bool) {
	t.Helper()
	var uid uuid.UUID
	var v any
	if verified {
		v = time.Now()
	}
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash, email_verified_at) VALUES ($1, 'x', $2) RETURNING id`, addr, v).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, $3)`, org, uid, role); err != nil {
		t.Fatal(err)
	}
}

// site adds a monitor with hourly checks for the first `hours` hours of September (none if hours is 0).
func (e *env) site(t *testing.T, org uuid.UUID, name string, hours int) {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, $2, 'https://example.com', 300) RETURNING id`, org, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if hours > 0 {
		if _, err := e.pool.Exec(t.Context(), `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms)
			SELECT $1, $2::timestamptz + g * interval '1 hour', $2::timestamptz + g * interval '1 hour', true, 200, 100 FROM generate_series(0, $3::int - 1) g`, id, sept, hours); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) logo(t *testing.T, org uuid.UUID) {
	t.Helper()
	var b bytes.Buffer
	img := imageOf(120, 40)
	if err := encodePNG(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := e.br.SaveName(t.Context(), org, "Northwind Digital"); err != nil {
		t.Fatal(err)
	}
	if err := e.br.SaveLogo(t.Context(), org, b.Bytes()); err != nil {
		t.Fatal(err)
	}
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

func TestEachOwnerGetsOneEmailWithAPDFPerWebsiteThatHadChecks(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "Acme Agency", "pro")
	e.member(t, org, "owner1@example.com", "owner", true)
	e.member(t, org, "owner2@example.com", "owner", true)
	e.member(t, org, "unverified@example.com", "owner", false)
	e.member(t, org, "staff@example.com", "member", true)
	e.site(t, org, "Client A", 100)
	e.site(t, org, "Client B", 200)
	e.site(t, org, "Paused all month", 0)

	if st := e.run(t, e.mail, 20, oct2); st.Emails != 2 {
		t.Fatalf("emails = %d, want 2 (one per verified owner)", st.Emails)
	}
	for _, addr := range []string{"owner1@example.com", "owner2@example.com"} {
		ms := e.to(addr)
		if len(ms) != 1 {
			t.Fatalf("%s got %d emails", addr, len(ms))
		}
		m := ms[0]
		if m.Subject != "Your September 2026 uptime reports" {
			t.Fatalf("subject = %q", m.Subject)
		}
		if len(m.Attachments) != 2 {
			t.Fatalf("attachments = %d, want 2: a site with no checks gets no report", len(m.Attachments))
		}
		names := m.Attachments[0].Filename + " " + m.Attachments[1].Filename
		if names != "client-a-2026-09.pdf client-b-2026-09.pdf" {
			t.Fatalf("names = %q", names)
		}
		for _, a := range m.Attachments {
			if a.ContentType != "application/pdf" || !bytes.HasPrefix(a.Data, []byte("%PDF-")) {
				t.Fatalf("%s is not a PDF", a.Filename)
			}
		}
		if !strings.Contains(m.Text, "Client A") || !strings.Contains(m.Text, "Client B") || strings.Contains(m.Text, "Paused all month") {
			t.Fatalf("the email should summarise each attached site:\n%s", m.Text)
		}
		if err := email.ValidateAttachments(m.Attachments); err != nil {
			t.Fatalf("the sender would refuse this email: %v", err)
		}
	}
	if len(e.to("unverified@example.com"))+len(e.to("staff@example.com")) != 0 {
		t.Fatal("only verified owners receive the reports")
	}
	// Done for the month: another pass sends nothing.
	if st := e.run(t, e.mail, 20, oct2.Add(5*time.Hour)); st.Emails != 0 || len(e.mail.Messages()) != 2 {
		t.Fatalf("sent again: %+v", st)
	}
}

func TestNothingIsSentBeforeTheMonthIsOverOrWhenSwitchedOff(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "Acme", "pro")
	e.member(t, org, "o@example.com", "owner", true)
	e.site(t, org, "A", 50)
	off := e.org(t, "Off", "pro")
	e.member(t, off, "off@example.com", "owner", true)
	e.site(t, off, "B", 50)
	e.br.SetMonthlyEmail(t.Context(), off, false)

	if st := e.run(t, e.mail, 20, justOver); st.Emails != 0 {
		t.Fatalf("sent %d minutes after the month ended; its last checks may not be in yet", 20)
	}
	if st := e.run(t, e.mail, 20, oct2); st.Emails != 1 || len(e.to("off@example.com")) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestAPlanWithoutReportsSendsNothingButIsNotMarkedDone(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "Acme", "free") // switched on at some point, then downgraded (or never upgraded)
	e.member(t, org, "o@example.com", "owner", true)
	e.site(t, org, "A", 50)

	if st := e.run(t, e.mail, 20, oct2); st.Emails != 0 {
		t.Fatal("a free plan does not include reports")
	}
	e.setPlan(t, org, "pro")
	if st := e.run(t, e.mail, 20, oct2.Add(time.Hour)); st.Emails != 1 {
		t.Fatalf("upgrading later in the month should still get that month's reports: %+v", st)
	}
}

func hasImage(pdf []byte) bool { return bytes.Contains(pdf, []byte("/Subtype /Image")) }

func TestOnlyAgencyReportsCarryTheLogo(t *testing.T) {
	e := newEnv(t)
	agency := e.org(t, "Agency", "agency")
	e.member(t, agency, "a@example.com", "owner", true)
	e.site(t, agency, "Site", 50)
	e.logo(t, agency)
	pro := e.org(t, "Pro", "pro")
	e.member(t, pro, "p@example.com", "owner", true)
	e.site(t, pro, "Site", 50)
	e.logo(t, pro) // stored before a downgrade, say

	e.run(t, e.mail, 20, oct2)
	a, p := e.to("a@example.com"), e.to("p@example.com")
	if len(a) != 1 || len(p) != 1 {
		t.Fatalf("emails: %d %d", len(a), len(p))
	}
	if !hasImage(a[0].Attachments[0].Data) {
		t.Fatal("the Agency report has no logo")
	}
	if hasImage(p[0].Attachments[0].Data) {
		t.Fatal("a Pro report must not carry the logo: branding is an Agency feature")
	}
	if !strings.Contains(a[0].Text, "They carry your name and logo") || strings.Contains(p[0].Text, "They carry your name and logo") {
		t.Fatal("the email should only say the reports are branded when they are")
	}
}

func TestALargeAccountGetsSeveralEmails(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "Big", "agency")
	e.member(t, org, "o@example.com", "owner", true)
	for i := 1; i <= 5; i++ {
		e.site(t, org, fmt.Sprintf("Site %d", i), 30)
	}
	e.run(t, e.mail, 2, oct2)
	ms := e.to("o@example.com")
	if len(ms) != 3 {
		t.Fatalf("emails = %d, want 3 (2 + 2 + 1 sites)", len(ms))
	}
	for i, want := range []string{"(part 1 of 3)", "(part 2 of 3)", "(part 3 of 3)"} {
		if !strings.Contains(ms[i].Subject, want) {
			t.Errorf("subject %d = %q", i, ms[i].Subject)
		}
	}
	if len(ms[0].Attachments) != 2 || len(ms[2].Attachments) != 1 {
		t.Fatalf("attachments per email: %d %d %d", len(ms[0].Attachments), len(ms[1].Attachments), len(ms[2].Attachments))
	}
}

type flaky struct {
	failFor map[string]error
	inner   *email.Memory
}

func (f flaky) Send(ctx context.Context, m email.Message) error {
	if err := f.failFor[m.To]; err != nil {
		return err
	}
	return f.inner.Send(ctx, m)
}

func TestATemporaryFailureIsRetriedWithoutResendingToThoseAlreadyServed(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "Acme", "pro")
	e.member(t, org, "first@example.com", "owner", true)
	e.member(t, org, "second@example.com", "owner", true)
	e.site(t, org, "A", 50)

	down := flaky{failFor: map[string]error{"second@example.com": errors.New("connection reset")}, inner: e.mail}
	if _, err := e.job(down, 20, oct2).RunOnce(context.Background()); err == nil {
		t.Fatal("a failed send must be reported so the pass is retried")
	}
	if len(e.to("first@example.com")) != 1 || len(e.to("second@example.com")) != 0 {
		t.Fatal("the first owner should already have theirs")
	}
	// The mail server recovers: only the second owner is sent to.
	if st := e.run(t, e.mail, 20, oct2.Add(time.Hour)); st.Emails != 1 {
		t.Fatalf("%+v", st)
	}
	if len(e.to("first@example.com")) != 1 || len(e.to("second@example.com")) != 1 {
		t.Fatalf("each owner should have exactly one: %d %d", len(e.to("first@example.com")), len(e.to("second@example.com")))
	}
}

func TestARefusedAddressIsNotRetried(t *testing.T) {
	e := newEnv(t)
	org := e.org(t, "Acme", "pro")
	e.member(t, org, "gone@example.com", "owner", true)
	e.member(t, org, "ok@example.com", "owner", true)
	e.site(t, org, "A", 50)
	refuse := flaky{failFor: map[string]error{"gone@example.com": fmt.Errorf("%w: mailbox does not exist", email.ErrPermanent)}, inner: e.mail}
	if _, err := e.job(refuse, 20, oct2).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.to("ok@example.com")) != 1 {
		t.Fatal("one bad address must not stop the others")
	}
	if st := e.run(t, e.mail, 20, oct2.Add(time.Hour)); st.Emails != 0 || len(e.to("gone@example.com")) != 0 {
		t.Fatalf("a refused address was tried again: %+v", st)
	}
}

type slow struct {
	*email.Memory
}

func (s slow) Send(ctx context.Context, m email.Message) error {
	time.Sleep(5 * time.Millisecond)
	return s.Memory.Send(ctx, m)
}

func TestSeveralWorkersNeverSendTheSameReportTwice(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 4; i++ {
		org := e.org(t, fmt.Sprintf("Org %d", i), "pro")
		e.member(t, org, fmt.Sprintf("o%d@example.com", i), "owner", true)
		e.site(t, org, "A", 30)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); e.job(slow{e.mail}, 20, oct2).RunOnce(context.Background()) }()
	}
	wg.Wait()
	seen := map[string]int{}
	for _, m := range e.mail.Messages() {
		seen[m.To]++
	}
	if len(seen) != 4 {
		t.Fatalf("%d owners mailed, want 4", len(seen))
	}
	for to, n := range seen {
		if n != 1 {
			t.Errorf("%s got %d copies", to, n)
		}
	}
}
