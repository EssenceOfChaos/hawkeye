package domains_test

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

	"hawkeye/internal/domains"
	"hawkeye/internal/email"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

// fakeLook answers lookups from a table and counts them.
type fakeLook struct {
	answers map[string]fakeAnswer
	calls   map[string]int
}

type fakeAnswer struct {
	expires     time.Time
	unavailable string
	err         error
}

func (f *fakeLook) Lookup(_ context.Context, name string) (domains.Result, error) {
	f.calls[name]++
	a, ok := f.answers[name]
	switch {
	case !ok:
		return domains.Result{}, errors.New("no answer scripted for " + name)
	case a.err != nil:
		return domains.Result{}, a.err
	case a.unavailable != "":
		return domains.Result{}, &domains.UnavailableError{Reason: a.unavailable}
	}
	return domains.Result{ExpiresAt: a.expires, Source: domains.SourceRDAP}, nil
}

func (f *fakeLook) expire(name string, at time.Time) { f.answers[name] = fakeAnswer{expires: at} }

type env struct {
	pool  *pgxpool.Pool
	plans *plans.Service
	svc   *domains.Service
	mons  *monitors.Service
	look  *fakeLook
	job   *domains.Job
	org   uuid.UUID
	clock time.Time
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newEnv(t *testing.T, plan string) *env {
	t.Helper()
	pool := testutil.NewPoolSize(t, 5)
	p := plans.NewService("http://app.test")
	e := &env{pool: pool, plans: p, svc: domains.NewService(pool, p),
		mons:  monitors.NewService(pool, p, monitors.WithSecretBox(testutil.SecretBox()), monitors.WithDomainSync(domains.SyncTx)),
		look:  &fakeLook{answers: map[string]fakeAnswer{}, calls: map[string]int{}},
		clock: time.Now().UTC().Truncate(time.Second)}
	e.svc.SetNow(func() time.Time { return e.clock })
	e.job = domains.NewJob(pool, e.look, domains.JobConfig{Recheck: 24 * time.Hour}, metrics.New(), quiet())
	e.job.SetNow(func() time.Time { return e.clock })
	e.org = e.newOrg(t, plan)
	return e
}

func (e *env) newOrg(t *testing.T, plan string) uuid.UUID {
	t.Helper()
	var org uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = $2`, org, plan); err != nil {
		t.Fatal(err)
	}
	return org
}

func (e *env) monitor(t *testing.T, org uuid.UUID, url string) monitors.Monitor {
	t.Helper()
	m, err := e.mons.Create(t.Context(), org, monitors.CreateInput{Name: "Site", URL: url})
	if err != nil {
		t.Fatalf("create monitor %s: %v", url, err)
	}
	return m
}

func (e *env) recipient(t *testing.T, org uuid.UUID, addr string, enabled, verified bool) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination, enabled, verified_at)
		VALUES ($1, 'email', $2, $3, CASE WHEN $4 THEN now() END)`, org, addr, enabled, verified); err != nil {
		t.Fatal(err)
	}
}

func (e *env) names(t *testing.T) []string {
	t.Helper()
	l, err := e.svc.List(t.Context(), e.org)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, d := range l.Domains {
		out = append(out, d.Name)
	}
	return out
}

func (e *env) domain(t *testing.T, name string) domains.Domain {
	t.Helper()
	l, err := e.svc.List(t.Context(), e.org)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range l.Domains {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("domain %q is not listed (have %v)", name, e.names(t))
	return domains.Domain{}
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) deliveries(t *testing.T) int {
	return e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = 'domain_expiry'`)
}

// pass makes every domain due and runs one pass at the given time.
func (e *env) pass(t *testing.T, at time.Time) domains.Stats {
	t.Helper()
	e.clock = at
	if _, err := e.pool.Exec(t.Context(), `UPDATE domains SET next_check_at = 'epoch'`); err != nil {
		t.Fatal(err)
	}
	st, err := e.job.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMonitorsDriveTheTrackedDomains(t *testing.T) {
	e := newEnv(t, "pro")
	a := e.monitor(t, e.org, "https://www.example.com")
	e.monitor(t, e.org, "https://api.example.com/health")
	e.monitor(t, e.org, "https://shop.example.co.uk")
	e.monitor(t, e.org, "https://mysite.vercel.app") // a name under a private suffix is not the customer's registration
	e.monitor(t, e.org, "https://203.0.113.9")
	eq(t, e.names(t), []string{"example.co.uk", "example.com"})
	if d := e.domain(t, "example.com"); d.MonitorCount != 2 || d.Manual || !d.Tracked || d.Status != domains.StatusPending {
		t.Fatalf("%+v", d)
	}

	// Pointing a monitor elsewhere moves the domain; deleting the last monitor on a domain drops it.
	other := "https://www.other.org"
	if _, err := e.mons.Update(t.Context(), e.org, a.ID, monitors.UpdateInput{URL: &other}); err != nil {
		t.Fatal(err)
	}
	eq(t, e.names(t), []string{"example.co.uk", "example.com", "other.org"})
	if d := e.domain(t, "example.com"); d.MonitorCount != 1 {
		t.Fatalf("%+v", d)
	}
	if err := e.mons.Delete(t.Context(), e.org, a.ID); err != nil {
		t.Fatal(err)
	}
	eq(t, e.names(t), []string{"example.co.uk", "example.com"})

	// Another organization's domains are its own.
	if l, _ := e.svc.List(t.Context(), e.newOrg(t, "free")); len(l.Domains) != 0 {
		t.Fatalf("leaked: %+v", l.Domains)
	}
}

func TestAddingAndRemovingDomainsByHand(t *testing.T) {
	e := newEnv(t, "free") // three domains
	d, err := e.svc.Add(t.Context(), e.org, "https://www.Foo.com/some/path")
	if err != nil || d.Name != "foo.com" || !d.Manual || !d.Tracked || d.Status != domains.StatusPending {
		t.Fatalf("%+v, %v", d, err)
	}
	if _, err := e.svc.Add(t.Context(), e.org, "bar.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Add(t.Context(), e.org, "baz.net"); err != nil {
		t.Fatal(err)
	}
	var le *plans.LimitError
	if _, err := e.svc.Add(t.Context(), e.org, "fourth.io"); !errors.As(err, &le) || le.Kind != plans.LimitDomains || le.Limit != 3 || le.Used != 3 {
		t.Fatalf("fourth domain: %v", err)
	}
	if _, err := e.svc.Add(t.Context(), e.org, "FOO.com"); err != nil { // already tracked: needs no free place
		t.Fatalf("re-adding: %v", err)
	}
	for _, bad := range []string{"", "localhost", "mysite.vercel.app", "203.0.113.9", "not a domain", "co.uk"} {
		var ve *validation.Error
		if _, err := e.svc.Add(t.Context(), e.org, bad); !errors.As(err, &ve) {
			t.Errorf("%q: %v, want a validation error", bad, err)
		}
	}

	if err := e.svc.Remove(t.Context(), e.org, d.ID); err != nil {
		t.Fatal(err)
	}
	eq(t, e.names(t), []string{"bar.org", "baz.net"})
	if err := e.svc.Remove(t.Context(), e.org, d.ID); !errors.Is(err, domains.ErrNotFound) {
		t.Fatalf("removing twice: %v", err)
	}
	if err := e.svc.Remove(t.Context(), e.org, uuid.New()); !errors.Is(err, domains.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestADomainUsedByMonitorsCannotBeRemoved(t *testing.T) {
	e := newEnv(t, "pro")
	mon := e.monitor(t, e.org, "https://example.com")
	d := e.domain(t, "example.com")
	if err := e.svc.Remove(t.Context(), e.org, d.ID); !errors.Is(err, domains.ErrInUse) {
		t.Fatalf("derived domain: %v", err)
	}
	// Adding it by hand pins it; removing it again only un-pins it while a monitor still uses it.
	if _, err := e.svc.Add(t.Context(), e.org, "example.com"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Remove(t.Context(), e.org, d.ID); err != nil {
		t.Fatal(err)
	}
	if d := e.domain(t, "example.com"); d.Manual {
		t.Fatalf("%+v", d)
	}
	// A pinned domain outlives its monitors.
	if _, err := e.svc.Add(t.Context(), e.org, "example.com"); err != nil {
		t.Fatal(err)
	}
	if err := e.mons.Delete(t.Context(), e.org, mon.ID); err != nil {
		t.Fatal(err)
	}
	if d := e.domain(t, "example.com"); !d.Manual || d.MonitorCount != 0 {
		t.Fatalf("%+v", d)
	}
}

func TestDomainsBeyondThePlanAreListedButNotLookedUp(t *testing.T) {
	e := newEnv(t, "free") // three domains
	for _, u := range []string{"https://one.com", "https://two.com", "https://three.com", "https://four.com", "https://five.com"} {
		e.monitor(t, e.org, u) // monitors always bring their domains; only adding by hand is limited
	}
	l, _ := e.svc.List(t.Context(), e.org)
	if len(l.Domains) != 5 || l.Limit != 3 {
		t.Fatalf("%d domains, limit %d", len(l.Domains), l.Limit)
	}
	tracked := map[string]bool{}
	for _, d := range l.Domains {
		if d.Tracked {
			tracked[d.Name] = true
		}
	}
	eq(t, []string{keys(tracked)}, []string{"one.com,three.com,two.com"}) // the oldest three
	for _, n := range []string{"one.com", "two.com", "three.com", "four.com", "five.com"} {
		e.look.expire(n, e.clock.Add(100*24*time.Hour))
	}
	if st := e.pass(t, e.clock); st.Known != 3 {
		t.Fatalf("%+v", st)
	}
	if e.look.calls["four.com"] != 0 || e.look.calls["five.com"] != 0 {
		t.Fatalf("looked up domains beyond the plan: %v", e.look.calls)
	}
	if d := e.domain(t, "four.com"); d.Tracked || d.Status != domains.StatusPending {
		t.Fatalf("%+v", d)
	}
	// Upgrading brings them in.
	e.pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code='pro'
		ON CONFLICT (organization_id) DO UPDATE SET plan_id = EXCLUDED.plan_id`, e.org)
	if st := e.pass(t, e.clock); st.Known != 5 {
		t.Fatalf("after upgrading: %+v", st)
	}
}

func keys(m map[string]bool) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	// sorted for a stable comparison
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return strings.Join(out, ",")
}

func TestWarningsAreQueuedOncePerBucketAndRecipient(t *testing.T) {
	e := newEnv(t, "pro")
	e.monitor(t, e.org, "https://example.com")
	e.recipient(t, e.org, "a@example.com", true, true)
	e.recipient(t, e.org, "b@example.com", true, true)
	e.recipient(t, e.org, "off@example.com", false, true)
	e.recipient(t, e.org, "new@example.com", true, false)
	expiry := e.clock.Add(45 * 24 * time.Hour)
	e.look.expire("example.com", expiry)

	e.pass(t, e.clock)
	if n := e.deliveries(t); n != 0 {
		t.Fatalf("45 days left must not warn (%d)", n)
	}
	d := e.domain(t, "example.com")
	if d.Status != domains.StatusValid || d.ExpiresAt == nil || !d.ExpiresAt.Equal(expiry) || d.DaysRemaining == nil || *d.DaysRemaining != 45 {
		t.Fatalf("%+v", d)
	}

	steps := []struct {
		daysLeft float64
		want     int // 2 recipients per bucket
	}{{29, 2}, {28, 2}, {13, 4}, {12, 4}, {6, 6}, {5, 6}, {0.5, 8}, {0.25, 8}}
	for _, s := range steps {
		e.pass(t, expiry.Add(-time.Duration(s.daysLeft*24*float64(time.Hour))))
		if n := e.deliveries(t); n != s.want {
			t.Fatalf("%.2f days left: %d deliveries, want %d", s.daysLeft, n, s.want)
		}
	}
	e.pass(t, expiry.Add(time.Hour))
	e.pass(t, expiry.Add(2*time.Hour))
	if n := e.deliveries(t); n != 10 {
		t.Fatalf("after expiry: %d deliveries, want 10", n)
	}
	if d := e.domain(t, "example.com"); d.Status != domains.StatusExpired {
		t.Fatalf("%+v", d)
	}
	for _, addr := range []string{"off@example.com", "new@example.com"} {
		if e.count(t, `SELECT count(*) FROM notification_deliveries d JOIN notification_settings s ON s.id = d.notification_setting_id
			WHERE d.event_type = 'domain_expiry' AND s.destination = $1`, addr) != 0 {
			t.Fatalf("%s must not receive domain warnings", addr)
		}
	}
}

func TestARenewedDomainStartsTheScheduleAgain(t *testing.T) {
	e := newEnv(t, "pro")
	e.monitor(t, e.org, "https://example.com")
	e.recipient(t, e.org, "a@example.com", true, true)
	e.look.expire("example.com", e.clock.Add(5*24*time.Hour))
	e.pass(t, e.clock) // inside the 7-day bucket
	if n := e.deliveries(t); n != 1 {
		t.Fatalf("%d", n)
	}
	renewed := e.clock.Add(370 * 24 * time.Hour)
	e.look.expire("example.com", renewed)
	e.pass(t, e.clock.Add(time.Hour))
	if n := e.deliveries(t); n != 1 {
		t.Fatalf("renewing must not send anything (%d)", n)
	}
	e.pass(t, renewed.Add(-20*24*time.Hour))
	if n := e.deliveries(t); n != 2 {
		t.Fatalf("%d deliveries after the renewed domain entered the 30-day bucket, want 2", n)
	}
}

func TestARecipientAddedLaterGetsNoBacklog(t *testing.T) {
	e := newEnv(t, "pro")
	e.monitor(t, e.org, "https://example.com")
	e.look.expire("example.com", e.clock.Add(10*24*time.Hour))
	e.pass(t, e.clock) // no recipient yet: the 14-day bucket is handled
	e.recipient(t, e.org, "late@example.com", true, true)
	e.pass(t, e.clock.Add(time.Hour))
	if n := e.deliveries(t); n != 0 {
		t.Fatalf("%d", n)
	}
}

func TestUnavailableAndFailedLookups(t *testing.T) {
	e := newEnv(t, "pro")
	e.monitor(t, e.org, "https://example.de")
	e.look.answers["example.de"] = fakeAnswer{unavailable: "The registry for .de domains does not publish expiry dates."}
	if st := e.pass(t, e.clock); st.Unavailable != 1 {
		t.Fatalf("%+v", st)
	}
	d := e.domain(t, "example.de")
	if d.Status != domains.StatusUnavailable || d.ExpiresAt != nil || d.UnavailableReason == nil || !strings.Contains(*d.UnavailableReason, ".de") || d.CheckedAt == nil {
		t.Fatalf("%+v", d)
	}

	// The registry starts answering.
	expiry := e.clock.Add(200 * 24 * time.Hour)
	e.look.expire("example.de", expiry)
	e.pass(t, e.clock)
	d = e.domain(t, "example.de")
	if d.Status != domains.StatusValid || d.UnavailableReason != nil {
		t.Fatalf("%+v", d)
	}

	// A failing lookup changes nothing the customer sees, apart from saying the lookups are failing, and is retried with a growing delay.
	e.look.answers["example.de"] = fakeAnswer{err: errors.New("rdap: timeout")}
	if st := e.pass(t, e.clock); st.Failed != 1 {
		t.Fatalf("%+v", st)
	}
	d = e.domain(t, "example.de")
	if d.Status != domains.StatusValid || d.ExpiresAt == nil || !d.ExpiresAt.Equal(expiry) || d.LookupError == nil || *d.LookupError != "rdap: timeout" {
		t.Fatalf("%+v", d)
	}
	var next time.Time
	var fails int
	e.pool.QueryRow(t.Context(), `SELECT next_check_at, failure_count FROM domains`).Scan(&next, &fails)
	if fails != 1 || !next.Equal(e.clock.Add(time.Hour)) {
		t.Fatalf("failure_count %d, next check %v (clock %v)", fails, next, e.clock)
	}
	e.pass(t, e.clock)
	e.pool.QueryRow(t.Context(), `SELECT next_check_at, failure_count FROM domains`).Scan(&next, &fails)
	if fails != 2 || !next.Equal(e.clock.Add(2*time.Hour)) {
		t.Fatalf("failure_count %d, next check %v", fails, next)
	}
	e.look.expire("example.de", expiry)
	e.pass(t, e.clock)
	if d = e.domain(t, "example.de"); d.LookupError != nil {
		t.Fatalf("%+v", d)
	}

	// Not due yet: a pass leaves it alone.
	before := e.look.calls["example.de"]
	e.clock = e.clock.Add(time.Hour)
	if st, err := e.job.RunOnce(t.Context()); err != nil || st.Known+st.Failed+st.Unavailable != 0 || e.look.calls["example.de"] != before {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestOneLookupPerNameServesEveryOrganization(t *testing.T) {
	e := newEnv(t, "pro")
	other := e.newOrg(t, "pro")
	e.monitor(t, e.org, "https://example.com")
	e.monitor(t, other, "https://www.example.com")
	e.look.expire("example.com", e.clock.Add(100*24*time.Hour))
	if st := e.pass(t, e.clock); st.Known != 2 {
		t.Fatalf("%+v", st)
	}
	if n := e.look.calls["example.com"]; n != 1 {
		t.Fatalf("%d lookups, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM domains WHERE status = 'known'`); n != 2 {
		t.Fatalf("%d", n)
	}
}

func TestThePassBackfillsDomainsOfExistingMonitors(t *testing.T) {
	e := newEnv(t, "pro")
	// A monitor that predates the feature, so nothing derived its domain.
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'Old', 'https://old.example.com', 60)`, e.org); err != nil {
		t.Fatal(err)
	}
	e.look.expire("example.com", e.clock.Add(100*24*time.Hour))
	e.job.SetNow(time.Now) // domains the pass creates are due the moment they exist
	if st, err := e.job.RunOnce(t.Context()); err != nil || st.Known != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	if d := e.domain(t, "example.com"); d.Status != domains.StatusValid || d.MonitorCount != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestOnlyOnePassRunsAtATime(t *testing.T) {
	e := newEnv(t, "pro")
	conn, err := e.pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var got bool
	// The key is the job's own constant, 0x48574b44.
	if err := conn.QueryRow(t.Context(), `SELECT pg_try_advisory_lock(1213682244)`).Scan(&got); err != nil || !got {
		t.Fatalf("%v %v", got, err)
	}
	st, err := e.job.RunOnce(t.Context())
	if err != nil || !st.Skipped {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestDeletingADomainRemovesItsQueuedWarnings(t *testing.T) {
	e := newEnv(t, "pro")
	d, err := e.svc.Add(t.Context(), e.org, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	e.recipient(t, e.org, "a@example.com", true, true)
	e.look.expire("example.com", e.clock.Add(3*24*time.Hour))
	e.pass(t, e.clock)
	if e.deliveries(t) != 1 {
		t.Fatal("setup")
	}
	if err := e.svc.Remove(t.Context(), e.org, d.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.deliveries(t); n != 0 {
		t.Fatalf("%d orphaned domain deliveries", n)
	}
}

func TestDomainWarningEmailIsDeliveredAndBranded(t *testing.T) {
	e := newEnv(t, "pro")
	e.monitor(t, e.org, "https://example.com")
	e.recipient(t, e.org, "owner@example.com", true, true)
	expiry := e.clock.Add(5 * 24 * time.Hour)
	e.look.expire("example.com", expiry)
	e.pass(t, e.clock)

	mail := &email.Memory{}
	disp := notifications.NewDispatcher(e.pool, mail, notifications.DispatcherConfig{AppBaseURL: "https://app.example.com", From: "alerts@mail.example.com"}, metrics.New(), quiet())
	if worked, err := disp.ProcessOne(t.Context()); err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	msgs := mail.Messages()
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	m := msgs[0]
	if m.To != "owner@example.com" || !strings.HasPrefix(m.Subject, "HawkEye reminder: the domain example.com expires in ") {
		t.Fatalf("%q to %q", m.Subject, m.To)
	}
	for _, want := range []string{"example.com", expiry.Format("2006-01-02 15:04 UTC"), "https://app.example.com/domains"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text missing %q:\n%s", want, m.Text)
		}
	}
	if !strings.Contains(m.HTML, "#ff8a80") {
		t.Error("a warning inside 7 days should use the alert colour")
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type='domain_expiry' AND status='sent'`); n != 1 {
		t.Fatalf("delivery not marked sent (%d)", n)
	}
}
