package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"hawkeye/internal/account"
	"hawkeye/internal/auth"
	"hawkeye/internal/billing"
	"hawkeye/internal/contact"
	"hawkeye/internal/email"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/httpapi"
	"hawkeye/internal/maintenance"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/ratelimit"
	"hawkeye/internal/statuspage"
	"hawkeye/internal/testutil"
)

type env struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
	mail *email.Memory
	fake *billing.FakeProvider // set when billing is enabled
	app  *echo.Echo
}

type envOpts struct {
	billingCfg billing.Config
	provider   billing.Provider

	// Phase 8 hardening, off by default so older tests keep their simple setup.
	shared    bool // use the database-backed shared rate limiter
	hsts      bool
	metrics   *metrics.APIMetrics
	reporter  errtrack.Reporter
	emailPerH int
	global    int
}

func newEnv(t *testing.T, rateLimit int) *env { return newEnvOpts(t, rateLimit, envOpts{}) }

// buildApp assembles the API against pool. It is called more than once on one pool to simulate several API processes.
func buildApp(t *testing.T, pool *pgxpool.Pool, mail *email.Memory, rateLimit int, o envOpts) *echo.Echo {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := auth.NewService(pool, mail, log, auth.Config{
		AppBaseURL: "http://app.test", Hasher: auth.Hasher{Memory: 8, Time: 1, Threads: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	planSvc := plans.NewService("http://app.test")
	opts := httpapi.Options{
		Logger: log, DB: pool, RequestTimeout: 10 * time.Second, BodyLimit: "1M",
		Auth: svc, Orgs: organizations.NewService(pool), AuthRateLimitPerMin: rateLimit,
		Plans: planSvc, Monitors: monitors.NewService(pool, planSvc, monitors.WithSecretBox(testutil.SecretBox())), Pool: pool, PgPool: pool,
		Notifications: notifications.NewService(pool, mail, log, "http://app.test", "alerts@test"),
		Maintenance:   maintenance.NewService(pool, planSvc),
		StatusPages:   statuspage.NewService(pool),
		Billing:       billing.NewService(pool, o.provider, o.billingCfg, metrics.New(), log),
		Account:       account.NewService(pool, svc, o.provider, mail, log, "https://app.example.com"),
		Contact:       contact.NewService(mail, log, "support@test.example", "alerts@test.example", "https://app.example.com"),
		HSTS:          o.hsts, Metrics: o.metrics, Errors: o.reporter, EmailActionLimitPerHour: o.emailPerH, GlobalRateLimitPerMin: o.global,
	}
	if o.shared {
		opts.Limiter = ratelimit.New(pool)
	}
	return httpapi.New(opts)
}

func newEnvOpts(t *testing.T, rateLimit int, o envOpts) *env {
	t.Helper()
	pool := testutil.NewPool(t)
	mail := &email.Memory{}
	app := buildApp(t, pool, mail, rateLimit, o)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	e := &env{srv: srv, pool: pool, mail: mail, app: app}
	if f, ok := o.provider.(*billing.FakeProvider); ok {
		e.fake = f
	}
	return e
}

// client is a browser-like client: it keeps cookies and echoes the CSRF cookie as a header.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (e *env) newClient(t *testing.T) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: e.srv.URL, http: &http.Client{Jar: jar}}
}

type resp struct {
	Status int
	Body   map[string]any
}

func (c *client) csrf() string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "csrf_token" {
			return ck.Value
		}
	}
	return ""
}

func (c *client) do(method, path string, body any, withCSRF bool) resp {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if withCSRF {
		req.Header.Set("X-CSRF-Token", c.csrf())
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return resp{res.StatusCode, m}
}

// call primes the CSRF cookie (as the SPA does with GET /auth/me) and sends a protected request.
func (c *client) call(method, path string, body any) resp {
	c.t.Helper()
	if c.csrf() == "" {
		c.do(http.MethodGet, "/api/v1/auth/me", nil, false)
	}
	return c.do(method, path, body, true)
}

func errCode(r resp) string {
	if e, ok := r.Body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

const pw = "a-long-enough-password"

func (e *env) register(t *testing.T, addr string) (*client, string) {
	t.Helper()
	c := e.newClient(t)
	r := c.call("POST", "/api/v1/auth/register", map[string]string{"email": addr, "password": pw})
	if r.Status != 201 {
		t.Fatalf("register %s: %d %v", addr, r.Status, r.Body)
	}
	orgs := c.call("GET", "/api/v1/organizations", nil)
	list := orgs.Body["organizations"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected one default org, got %v", orgs.Body)
	}
	return c, list[0].(map[string]any)["id"].(string)
}

var tokenRe = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

func (e *env) lastToken(t *testing.T, to, subjectContains string) string {
	t.Helper()
	msgs := e.mail.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].To == to && strings.Contains(msgs[i].Subject, subjectContains) {
			if m := tokenRe.FindStringSubmatch(msgs[i].Text); m != nil {
				return m[1]
			}
		}
	}
	t.Fatalf("no %q email for %s", subjectContains, to)
	return ""
}

func TestRegisterLoginLogoutMe(t *testing.T) {
	e := newEnv(t, 1000)
	c, orgID := e.register(t, "Alice@Example.com")

	me := c.call("GET", "/api/v1/auth/me", nil)
	user := me.Body["user"].(map[string]any)
	if me.Status != 200 || user["email"] != "alice@example.com" || user["email_verified_at"] != nil {
		t.Fatalf("me: %d %v", me.Status, me.Body)
	}
	org := c.call("GET", "/api/v1/organizations/"+orgID, nil).Body["organization"].(map[string]any)
	if org["role"] != "owner" {
		t.Fatalf("expected owner role, got %v", org)
	}

	if r := c.call("POST", "/api/v1/auth/logout", nil); r.Status != 204 {
		t.Fatalf("logout: %d", r.Status)
	}
	if r := c.call("GET", "/api/v1/auth/me", nil); r.Status != 401 {
		t.Fatalf("session should be revoked, got %d", r.Status)
	}

	if r := c.call("POST", "/api/v1/auth/login", map[string]string{"email": "alice@example.com", "password": "wrong-password-1"}); r.Status != 401 || errCode(r) != "INVALID_CREDENTIALS" {
		t.Fatalf("bad login: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/auth/login", map[string]string{"email": "nobody@example.com", "password": pw}); r.Status != 401 || errCode(r) != "INVALID_CREDENTIALS" {
		t.Fatalf("unknown user login: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/auth/login", map[string]string{"email": "ALICE@example.com", "password": pw}); r.Status != 200 {
		t.Fatalf("login: %d %v", r.Status, r.Body)
	}
	if r := c.call("GET", "/api/v1/auth/me", nil); r.Status != 200 {
		t.Fatalf("me after login: %d", r.Status)
	}
}

func TestRegisterValidationAndDuplicates(t *testing.T) {
	e := newEnv(t, 1000)
	c := e.newClient(t)
	r := c.call("POST", "/api/v1/auth/register", map[string]string{"email": "bad", "password": "short"})
	if r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
		t.Fatalf("got %d %v", r.Status, r.Body)
	}
	e.register(t, "dup@example.com")
	r = c.call("POST", "/api/v1/auth/register", map[string]string{"email": "DUP@example.com", "password": pw})
	if r.Status != 409 || errCode(r) != "EMAIL_TAKEN" {
		t.Fatalf("got %d %v", r.Status, r.Body)
	}
	var n int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM organizations`).Scan(&n)
	if n != 1 {
		t.Fatalf("failed registration must not leave organizations behind, have %d", n)
	}
	if r := c.call("POST", "/api/v1/auth/register", nil); r.Status != 400 {
		t.Fatalf("empty body: %d", r.Status)
	}
}

func TestUnauthenticatedAccessRejected(t *testing.T) {
	e := newEnv(t, 1000)
	c := e.newClient(t)
	for _, p := range []string{"/api/v1/auth/me", "/api/v1/organizations", "/api/v1/organizations/00000000-0000-0000-0000-000000000000"} {
		if r := c.call("GET", p, nil); r.Status != 401 || errCode(r) != "UNAUTHENTICATED" {
			t.Errorf("%s: %d %v", p, r.Status, r.Body)
		}
	}
}

func TestCSRFRequiredOnUnsafeMethods(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "csrf@example.com")
	r := c.do("POST", "/api/v1/auth/logout", nil, false)
	if r.Status != 403 || errCode(r) != "CSRF_INVALID" {
		t.Fatalf("got %d %v", r.Status, r.Body)
	}
	if r := c.call("GET", "/api/v1/auth/me", nil); r.Status != 200 {
		t.Fatalf("rejected CSRF request must not have logged out: %d", r.Status)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	e := newEnv(t, 1000)
	c := e.newClient(t)
	c.call("GET", "/api/v1/auth/me", nil) // prime CSRF
	b, _ := json.Marshal(map[string]string{"email": "ck@example.com", "password": pw})
	req, _ := http.NewRequest("POST", c.base+"/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf())
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	for _, ck := range res.Cookies() {
		if ck.Name == "hawkeye_session" {
			if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.MaxAge <= 0 {
				t.Fatalf("weak session cookie: %+v", ck)
			}
			return
		}
	}
	t.Fatal("no session cookie set")
}

// The Phase 2 acceptance criterion: guessing identifiers must not expose another tenant's data.
func TestOrganizationIsolation(t *testing.T) {
	e := newEnv(t, 1000)
	alice, aliceOrg := e.register(t, "alice@example.com")
	bob, bobOrg := e.register(t, "bob@example.com")

	if r := alice.call("GET", "/api/v1/organizations/"+aliceOrg, nil); r.Status != 200 {
		t.Fatalf("own org: %d", r.Status)
	}
	for name, call := range map[string]func() resp{
		"get":     func() resp { return bob.call("GET", "/api/v1/organizations/"+aliceOrg, nil) },
		"members": func() resp { return bob.call("GET", "/api/v1/organizations/"+aliceOrg+"/members", nil) },
		"patch": func() resp {
			return bob.call("PATCH", "/api/v1/organizations/"+aliceOrg, map[string]string{"name": "pwned"})
		},
		"garbage": func() resp { return bob.call("GET", "/api/v1/organizations/not-a-uuid", nil) },
	} {
		if r := call(); r.Status != 404 || errCode(r) != "NOT_FOUND" {
			t.Errorf("%s: want 404 NOT_FOUND, got %d %v", name, r.Status, r.Body)
		}
	}
	list := bob.call("GET", "/api/v1/organizations", nil).Body["organizations"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != bobOrg {
		t.Fatalf("bob sees %v", list)
	}
	var name string
	_ = e.pool.QueryRow(t.Context(), `SELECT name FROM organizations WHERE id = $1`, aliceOrg).Scan(&name)
	if name == "pwned" {
		t.Fatal("cross-tenant update succeeded")
	}
}

func TestOrganizationRenameAndMemberRole(t *testing.T) {
	e := newEnv(t, 1000)
	alice, org := e.register(t, "alice@example.com")
	r := alice.call("PATCH", "/api/v1/organizations/"+org, map[string]string{"name": "  Acme Agency "})
	if r.Status != 200 || r.Body["organization"].(map[string]any)["name"] != "Acme Agency" {
		t.Fatalf("rename: %d %v", r.Status, r.Body)
	}
	if r := alice.call("PATCH", "/api/v1/organizations/"+org, map[string]string{"name": ""}); r.Status != 400 {
		t.Fatalf("empty name: %d", r.Status)
	}
	if r := alice.call("GET", "/api/v1/organizations/"+org+"/members", nil); r.Status != 200 || len(r.Body["members"].([]any)) != 1 {
		t.Fatalf("members: %d %v", r.Status, r.Body)
	}

	// A plain member can read but not rename.
	bob, _ := e.register(t, "bob@example.com")
	_, err := e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role)
		SELECT $1, id, 'member' FROM users WHERE email = 'bob@example.com'`, org)
	if err != nil {
		t.Fatal(err)
	}
	if r := bob.call("GET", "/api/v1/organizations/"+org, nil); r.Status != 200 {
		t.Fatalf("member read: %d", r.Status)
	}
	if r := bob.call("PATCH", "/api/v1/organizations/"+org, map[string]string{"name": "x"}); r.Status != 403 || errCode(r) != "FORBIDDEN" {
		t.Fatalf("member rename: %d %v", r.Status, r.Body)
	}
}

func TestEmailVerification(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "v@example.com")
	tok := e.lastToken(t, "v@example.com", "Verify")

	if r := c.call("POST", "/api/v1/auth/verify-email", map[string]string{"token": "garbage"}); r.Status != 400 || errCode(r) != "INVALID_TOKEN" {
		t.Fatalf("bad token: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/auth/verify-email", map[string]string{"token": tok}); r.Status != 204 {
		t.Fatalf("verify: %d", r.Status)
	}
	if r := c.call("POST", "/api/v1/auth/verify-email", map[string]string{"token": tok}); r.Status != 400 {
		t.Fatalf("token reuse must fail: %d", r.Status)
	}
	if u := c.call("GET", "/api/v1/auth/me", nil).Body["user"].(map[string]any); u["email_verified_at"] == nil {
		t.Fatal("email not marked verified")
	}
}

func TestResendVerificationInvalidatesOldLink(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "r@example.com")
	old := e.lastToken(t, "r@example.com", "Verify")
	if r := c.call("POST", "/api/v1/auth/resend-verification", nil); r.Status != 202 {
		t.Fatalf("resend: %d", r.Status)
	}
	if r := c.call("POST", "/api/v1/auth/verify-email", map[string]string{"token": old}); r.Status != 400 {
		t.Fatalf("old link should be dead: %d", r.Status)
	}
	fresh := e.lastToken(t, "r@example.com", "Verify")
	if r := c.call("POST", "/api/v1/auth/verify-email", map[string]string{"token": fresh}); r.Status != 204 {
		t.Fatalf("new link: %d", r.Status)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "p@example.com")
	other := e.newClient(t)

	// Unknown and known addresses get the same response; only the known one gets mail.
	r1 := other.call("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "ghost@example.com"})
	r2 := other.call("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "p@example.com"})
	if r1.Status != 202 || r2.Status != 202 {
		t.Fatalf("statuses %d %d", r1.Status, r2.Status)
	}
	for _, m := range e.mail.Messages() {
		if m.To == "ghost@example.com" {
			t.Fatal("mail sent to unknown address")
		}
	}
	tok := e.lastToken(t, "p@example.com", "Reset")

	if r := other.call("POST", "/api/v1/auth/reset-password", map[string]string{"token": tok, "password": "short"}); r.Status != 400 {
		t.Fatalf("weak password: %d", r.Status)
	}
	newPw := "brand-new-password-1"
	if r := other.call("POST", "/api/v1/auth/reset-password", map[string]string{"token": tok, "password": newPw}); r.Status != 204 {
		t.Fatalf("reset: %d", r.Status)
	}
	if r := other.call("POST", "/api/v1/auth/reset-password", map[string]string{"token": tok, "password": newPw}); r.Status != 400 {
		t.Fatalf("token reuse: %d", r.Status)
	}
	if r := c.call("GET", "/api/v1/auth/me", nil); r.Status != 401 {
		t.Fatalf("existing sessions must be revoked, got %d", r.Status)
	}
	if r := other.call("POST", "/api/v1/auth/login", map[string]string{"email": "p@example.com", "password": pw}); r.Status != 401 {
		t.Fatalf("old password still works: %d", r.Status)
	}
	if r := other.call("POST", "/api/v1/auth/login", map[string]string{"email": "p@example.com", "password": newPw}); r.Status != 200 {
		t.Fatalf("new password: %d", r.Status)
	}
	if u := other.call("GET", "/api/v1/auth/me", nil).Body["user"].(map[string]any); u["email_verified_at"] == nil {
		t.Fatal("reset should verify the address")
	}
}

func TestSecondResetInvalidatesFirst(t *testing.T) {
	e := newEnv(t, 1000)
	e.register(t, "s@example.com")
	c := e.newClient(t)
	c.call("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "s@example.com"})
	first := e.lastToken(t, "s@example.com", "Reset")
	c.call("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "s@example.com"})
	if r := c.call("POST", "/api/v1/auth/reset-password", map[string]string{"token": first, "password": "another-long-password"}); r.Status != 400 {
		t.Fatalf("superseded token accepted: %d", r.Status)
	}
}

func TestExpiredSessionAndDisabledUser(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "x@example.com")
	if _, err := e.pool.Exec(t.Context(), `UPDATE sessions SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if r := c.call("GET", "/api/v1/auth/me", nil); r.Status != 401 {
		t.Fatalf("expired session accepted: %d", r.Status)
	}

	c2 := e.newClient(t)
	if r := c2.call("POST", "/api/v1/auth/login", map[string]string{"email": "x@example.com", "password": pw}); r.Status != 200 {
		t.Fatalf("login: %d", r.Status)
	}
	_, _ = e.pool.Exec(t.Context(), `UPDATE users SET disabled_at = now()`)
	if r := c2.call("GET", "/api/v1/auth/me", nil); r.Status != 401 {
		t.Fatalf("disabled user kept access: %d", r.Status)
	}
	if r := c2.call("POST", "/api/v1/auth/login", map[string]string{"email": "x@example.com", "password": pw}); r.Status != 401 {
		t.Fatalf("disabled user logged in: %d", r.Status)
	}
}

func TestAuthRateLimit(t *testing.T) {
	e := newEnv(t, 3)
	c := e.newClient(t)
	var last resp
	for range 6 {
		last = c.call("POST", "/api/v1/auth/login", map[string]string{"email": "a@example.com", "password": "whatever-password"})
	}
	if last.Status != 429 || errCode(last) != "RATE_LIMITED" {
		t.Fatalf("expected 429, got %d %v", last.Status, last.Body)
	}
}

func TestSessionTokenStoredHashed(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "h@example.com")
	u, _ := url.Parse(c.base)
	var raw string
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "hawkeye_session" {
			raw = ck.Value
		}
	}
	var n int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE token_hash = $1`, []byte(raw)).Scan(&n)
	if raw == "" || n != 0 {
		t.Fatal("raw session token must not be stored")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// doWithHeader sends a GET with X-Organization-ID and returns the status code.
func (c *client) doWithHeader(method, path, orgHeader string) int {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, nil)
	req.Header.Set("X-Organization-ID", orgHeader)
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// doWithHeaderBody sends a JSON body with X-Organization-ID (and the CSRF header) and returns the status.
func (c *client) doWithHeaderBody(method, path, orgHeader, body string) int {
	c.t.Helper()
	if c.csrf() == "" {
		c.do(http.MethodGet, "/api/v1/auth/me", nil, false)
	}
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf())
	req.Header.Set("X-Organization-ID", orgHeader)
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func mustJSONBytes(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
