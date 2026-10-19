// Package e2e holds one acceptance test that walks the plan's "definition of MVP completion" (section 17) through the real system:
// HTTP API, PostgreSQL, scheduler, check workers, incident engine, notification dispatcher, billing webhooks, account deletion.
// Only the outside world is simulated: the monitored website (a local test server), the mailbox (in memory) and Stripe (signed webhooks).
package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/account"
	"hawkeye/internal/auth"
	"hawkeye/internal/billing"
	"hawkeye/internal/billing/billingtest"
	"hawkeye/internal/checks"
	"hawkeye/internal/email"
	"hawkeye/internal/httpapi"
	"hawkeye/internal/incidents"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/ratelimit"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/testutil"
	"hawkeye/internal/worker"
)

type stack struct {
	t      *testing.T
	pool   *pgxpool.Pool
	srv    *httptest.Server
	mail   *email.Memory
	sched  *scheduler.Scheduler
	worker *worker.Worker
	disp   *notifications.Dispatcher
	m      *metrics.Metrics
	fake   *billing.FakeProvider
}

func newStack(t *testing.T) *stack {
	t.Helper()
	pool := testutil.NewPoolSize(t, 20)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mail := &email.Memory{}
	m := metrics.New()
	authSvc, err := auth.NewService(pool, mail, log, auth.Config{AppBaseURL: "https://app.test", Hasher: auth.Hasher{Memory: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	planSvc := plans.NewService("https://app.test")
	fake := &billing.FakeProvider{Subscriptions: map[string]billing.SubscriptionState{}}
	bcfg := billing.Config{SecretKey: "sk_test_x", WebhookSecret: billingtest.Secret, PricePro: "price_pro", PriceAgency: "price_agency", AppBaseURL: "https://app.test"}
	app := httpapi.New(httpapi.Options{
		Logger: log, DB: pool, RequestTimeout: 10 * time.Second, BodyLimit: "1M", AuthRateLimitPerMin: 1000,
		Auth: authSvc, Orgs: organizations.NewService(pool), Plans: planSvc, Monitors: monitors.NewService(pool, planSvc, monitors.WithSecretBox(testutil.SecretBox())), Pool: pool, PgPool: pool,
		Notifications: notifications.NewService(pool, mail, log, "https://app.test", "alerts@hawkeye.test"),
		Billing:       billing.NewService(pool, fake, bcfg, m, log), Account: account.NewService(pool, authSvc, fake, mail, log, "https://app.example.com"),
		Limiter: ratelimit.New(pool), Metrics: metrics.NewAPI(), HSTS: true, EmailActionLimitPerHour: 50,
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	// The checker may reach the local test website (127.0.0.1) but nothing else private: the same rules as production otherwise.
	cfg := safehttp.Config{
		AllowIP:   func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") || safehttp.IsPublicIP(a) },
		AllowPort: func(int) bool { return true },
	}
	return &stack{
		t: t, pool: pool, srv: srv, mail: mail, m: m, fake: fake,
		sched:  scheduler.New(pool, scheduler.Config{}, m, log),
		worker: worker.New(pool, checks.NewChecker(cfg), checks.NewStore(pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{}), worker.Config{Concurrency: 1}, m, log),
		disp:   notifications.NewDispatcher(pool, mail, notifications.DispatcherConfig{AppBaseURL: "https://app.test", From: "alerts@hawkeye.test"}, m, log),
	}
}

// runChecks makes every monitor due now and runs the scheduler, the workers and the alert dispatcher until idle.
func (s *stack) runChecks() {
	s.t.Helper()
	s.pool.Exec(s.t.Context(), `UPDATE monitors SET next_check_at = now() WHERE enabled AND deleted_at IS NULL`)
	if _, err := s.sched.Tick(s.t.Context()); err != nil {
		s.t.Fatal(err)
	}
	for {
		worked, err := s.worker.ProcessOne(s.t.Context())
		if err != nil {
			s.t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	for {
		worked, err := s.disp.ProcessOne(s.t.Context())
		if err != nil {
			s.t.Fatal(err)
		}
		if !worked {
			break
		}
	}
}

// browser is a minimal cookie-and-CSRF-aware API client.
type browser struct {
	t    *testing.T
	base string
	c    *http.Client
}

func (s *stack) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: s.t, base: s.srv.URL, c: &http.Client{Jar: jar}}
}

type reply struct {
	Status int
	Body   map[string]any
}

func (b *browser) do(method, path string, body any) reply {
	b.t.Helper()
	csrf := func() string {
		for _, ck := range b.c.Jar.Cookies(mustURL(b.base)) {
			if ck.Name == "csrf_token" {
				return ck.Value
			}
		}
		return ""
	}
	if csrf() == "" {
		if res, err := b.c.Get(b.base + "/api/v1/auth/me"); err == nil {
			res.Body.Close()
		}
	}
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, b.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf())
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return reply{res.StatusCode, m}
}

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }

func (r reply) obj(keys ...string) map[string]any {
	var cur any = r.Body
	for _, k := range keys {
		cur = cur.(map[string]any)[k]
	}
	return cur.(map[string]any)
}

var tokenRe = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

func (s *stack) lastToken(to, subject string) string {
	msgs := s.mail.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].To == to && strings.Contains(msgs[i].Subject, subject) {
			return tokenRe.FindStringSubmatch(msgs[i].Text)[1]
		}
	}
	s.t.Fatalf("no %q email to %s", subject, to)
	return ""
}

func (s *stack) mailsTo(to, subject string) int {
	n := 0
	for _, m := range s.mail.Messages() {
		if m.To == to && strings.Contains(m.Subject, subject) {
			n++
		}
	}
	return n
}

func TestMVPDefinitionOfDone(t *testing.T) {
	s := newStack(t)
	var status atomic.Int32
	status.Store(200)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer site.Close()
	const owner, pw = "owner@agency.test", "a-long-enough-password"
	me := s.browser()

	// 1. Register and verify the email.
	if r := me.do("POST", "/api/v1/auth/register", map[string]string{"email": owner, "password": pw}); r.Status != 201 {
		t.Fatalf("register: %d %v", r.Status, r.Body)
	}
	if r := me.do("POST", "/api/v1/auth/verify-email", map[string]string{"token": s.lastToken(owner, "Verify")}); r.Status != 204 {
		t.Fatalf("verify: %d", r.Status)
	}

	// 2. Sign in and add websites. (The API refuses 127.0.0.1 targets, so the local test site is inserted directly, as the worker must cope anyway.)
	orgID := me.do("GET", "/api/v1/organizations", nil).Body["organizations"].([]any)[0].(map[string]any)["id"].(string)
	var siteID string
	if err := s.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, timeout_seconds)
		VALUES ($1, 'Client site', $2, 300, 5) RETURNING id`, orgID, site.URL).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if r := me.do("POST", "/api/v1/monitors", map[string]string{"name": "Real one", "url": "https://example.com"}); r.Status != 201 {
		t.Fatalf("create via API: %d %v", r.Status, r.Body)
	}
	if r := me.do("POST", "/api/v1/monitors", map[string]string{"name": "Sneaky", "url": "http://169.254.169.254/"}); r.Status != 400 {
		t.Fatalf("an internal address must be refused by the API: %d", r.Status)
	}

	// 3. Current status and response times appear after the first check.
	s.pool.Exec(t.Context(), `UPDATE monitors SET enabled = false WHERE name = 'Real one'`) // do not touch the internet from a test
	s.runChecks()
	sum := me.do("GET", "/api/v1/dashboard/summary", nil).obj("summary")
	if sum["monitors"].(map[string]any)["up"].(float64) != 1 || sum["open_incidents"].(float64) != 0 {
		t.Fatalf("summary after first check: %v", sum)
	}
	if got := me.do("GET", "/api/v1/monitors/"+siteID, nil).obj("monitor")["current_status"]; got != "up" {
		t.Fatalf("status %v", got)
	}
	if pts := me.do("GET", "/api/v1/monitors/"+siteID+"/response-times", nil).Body["points"].([]any); len(pts) == 0 {
		t.Fatal("no response-time data")
	}

	// 4. A one-off blip does not alert; a confirmed outage sends exactly one email.
	status.Store(503)
	s.runChecks()
	if s.mailsTo(owner, "may be offline") != 0 {
		t.Fatal("one failed check must not send an alert")
	}
	s.runChecks()
	if n := s.mailsTo(owner, "may be offline"); n != 1 {
		t.Fatalf("confirmed outage must send exactly one email, sent %d", n)
	}
	s.runChecks()
	s.runChecks()
	if n := s.mailsTo(owner, "may be offline"); n != 1 {
		t.Fatalf("an ongoing outage must not send more email, sent %d", n)
	}
	sum = me.do("GET", "/api/v1/dashboard/summary", nil).obj("summary")
	if sum["monitors"].(map[string]any)["down"].(float64) != 1 || sum["open_incidents"].(float64) != 1 {
		t.Fatalf("summary during outage: %v", sum)
	}

	// 5. Recovery closes the incident and notifies; history and uptime are available.
	status.Store(200)
	s.runChecks()
	if s.mailsTo(owner, "is back online") != 0 {
		t.Fatal("one success must not announce recovery")
	}
	s.runChecks()
	if n := s.mailsTo(owner, "is back online"); n != 1 {
		t.Fatalf("recovery emails: %d", n)
	}
	inc := me.do("GET", "/api/v1/monitors/"+siteID+"/incidents", nil).Body["incidents"].([]any)
	if len(inc) != 1 || inc[0].(map[string]any)["status"] != "resolved" {
		t.Fatalf("incident history: %v", inc)
	}
	up := me.do("GET", "/api/v1/monitors/"+siteID+"/uptime?range=24h", nil).obj("uptime")
	if up["method"] != "check_based" || up["total_checks"].(float64) != 7 || up["failed_checks"].(float64) != 4 || up["uptime_percent"].(float64) != 42.857 {
		t.Fatalf("uptime must be successful/total = 3/7 = 42.857%%: %v", up)
	}

	// 6. Upgrade through a verified payment-provider event; the backend raises the limits. A forged event does not.
	forged := billingtest.SubscriptionEvent("evt_forged", "customer.subscription.created", time.Now(), billingtest.Sub{ID: "sub_f", Customer: "cus_f", Status: "active", Price: "price_pro", OrgID: orgID})
	if code := s.webhook(forged, billingtest.Sign(forged, "whsec_attacker", time.Now())); code != 400 {
		t.Fatalf("forged webhook: %d", code)
	}
	if got := me.do("GET", "/api/v1/usage", nil).obj("usage")["monitor_limit"]; got != float64(5) {
		t.Fatalf("limit before upgrade: %v", got)
	}
	sub := billingtest.Sub{ID: "sub_1", Customer: "cus_1", Status: "active", Price: "price_pro", OrgID: orgID, PeriodEnd: time.Now().Add(720 * time.Hour).Unix()}
	s.upgrade(sub, "evt_up")
	if got := me.do("GET", "/api/v1/usage", nil).obj("usage")["monitor_limit"]; got != float64(50) {
		t.Fatalf("limit after upgrade: %v", got)
	}

	// 7. Cancel: the subscription ends, access to the account and ALL monitors remain; only new monitors are limited by the Free plan.
	for i := 0; i < 6; i++ {
		if r := me.do("POST", "/api/v1/monitors", map[string]string{"name": "extra", "url": "https://example.org"}); r.Status != 201 {
			t.Fatalf("paid monitor %d: %d", i, r.Status)
		}
	}
	sub.Status = "canceled"
	s.upgrade(sub, "evt_end")
	if got := me.do("GET", "/api/v1/monitors", nil).Body["total"].(float64); got != 8 {
		t.Fatalf("monitors after cancellation: %v", got)
	}
	if r := me.do("POST", "/api/v1/monitors", map[string]string{"name": "blocked", "url": "https://example.net"}); r.Status != 403 || r.obj("error")["code"] != "PLAN_LIMIT_REACHED" {
		t.Fatalf("over the Free limit: %d %v", r.Status, r.Body)
	}
	if r := me.do("GET", "/api/v1/auth/me", nil); r.Status != 200 {
		t.Fatal("cancelling must not lock the user out")
	}

	// 8. Privacy: export, then delete everything.
	if r := me.do("GET", "/api/v1/account/export", nil); r.Status != 200 {
		t.Fatalf("export: %d", r.Status)
	}
	if r := me.do("POST", "/api/v1/account/delete", map[string]string{"password": pw, "confirm_email": owner}); r.Status != 204 {
		t.Fatalf("delete: %d %v", r.Status, r.Body)
	}
	for _, q := range []string{`SELECT count(*) FROM users`, `SELECT count(*) FROM organizations`, `SELECT count(*) FROM monitors`, `SELECT count(*) FROM check_results`, `SELECT count(*) FROM incidents`, `SELECT count(*) FROM notification_settings`} {
		var n int
		s.pool.QueryRow(t.Context(), q).Scan(&n)
		if n != 0 {
			t.Errorf("%s = %d after deleting the only account", q, n)
		}
	}
	if r := s.browser().do("POST", "/api/v1/auth/login", map[string]string{"email": owner, "password": pw}); r.Status != 401 {
		t.Fatalf("deleted account can still sign in: %d", r.Status)
	}
}

func (s *stack) webhook(payload []byte, sig string) int {
	req, _ := http.NewRequest("POST", s.srv.URL+"/api/v1/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", sig)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// upgrade delivers a correctly signed subscription event, with the provider's current state set to match it (as Stripe's would be).
func (s *stack) upgrade(sub billingtest.Sub, id string) {
	s.t.Helper()
	typ := "customer.subscription.updated"
	if sub.Status == "canceled" {
		typ = "customer.subscription.deleted"
	}
	payload := billingtest.SubscriptionEvent(id, typ, time.Now(), sub)
	billingtest.Mirror(s.fake, payload)
	if code := s.webhook(payload, billingtest.Sign(payload, billingtest.Secret, time.Now())); code != 200 {
		s.t.Fatalf("webhook %s: %d", id, code)
	}
}
