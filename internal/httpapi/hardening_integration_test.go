package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/metrics"
)

// ---- shared (cross-process) rate limiting

func TestAuthRateLimitIsSharedAcrossApiProcesses(t *testing.T) {
	e := newEnvOpts(t, 4, envOpts{shared: true}) // 4 requests/minute/IP/endpoint
	second := httptest.NewServer(buildApp(t, e.pool, e.mail, 4, envOpts{shared: true}))
	defer second.Close()

	a := e.newClient(t)
	b := &client{t: t, base: second.URL, http: a.http} // same browser, other server
	body := map[string]string{"email": "nobody@example.com", "password": "whatever-password"}

	var statuses []int
	for i := 0; i < 6; i++ {
		c := a
		if i%2 == 1 {
			c = b // alternate between the two "processes"
		}
		statuses = append(statuses, c.call("POST", "/api/v1/auth/login", body).Status)
	}
	// 4 allowed (401 invalid credentials), then limited, regardless of which process served the request.
	want := []int{401, 401, 401, 401, 429, 429}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("statuses %v, want %v: the budget must be shared by both processes", statuses, want)
		}
	}
}

func TestRateLimitedResponsesCarryRetryAfter(t *testing.T) {
	e := newEnvOpts(t, 1, envOpts{shared: true})
	c := e.newClient(t)
	c.call("POST", "/api/v1/auth/login", map[string]string{"email": "a@example.com", "password": "x-password-123"})
	c.call("GET", "/api/v1/auth/me", nil) // prime csrf
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/auth/login", strings.NewReader(`{"email":"a@example.com","password":"x-password-123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf())
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	ra, _ := strconv.Atoi(res.Header.Get("Retry-After"))
	if res.StatusCode != 429 || ra < 1 || ra > 61 {
		t.Fatalf("status %d Retry-After %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
}

func TestEndpointsHaveIndependentBudgets(t *testing.T) {
	e := newEnvOpts(t, 2, envOpts{shared: true})
	c := e.newClient(t)
	for range 3 {
		c.call("POST", "/api/v1/auth/login", map[string]string{"email": "a@example.com", "password": "x-password-123"})
	}
	if r := c.call("POST", "/api/v1/auth/login", map[string]string{"email": "a@example.com", "password": "x-password-123"}); r.Status != 429 {
		t.Fatalf("login should be limited: %d", r.Status)
	}
	if r := c.call("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "a@example.com"}); r.Status != 202 {
		t.Fatalf("exhausting login must not block password reset requests: %d", r.Status)
	}
}

func TestPerAccountLoginCeilingStopsRotatingIPCredentialStuffing(t *testing.T) {
	e := newEnvOpts(t, 100000, envOpts{shared: true}) // per-IP limit out of the way
	c := e.newClient(t)
	var limited int
	for i := 0; i < 34; i++ {
		if c.call("POST", "/api/v1/auth/login", map[string]string{"email": "victim@example.com", "password": "guess-number-" + strconv.Itoa(i)}).Status == 429 {
			limited++
		}
	}
	if limited != 4 { // the first 30 attempts are processed, the rest refused
		t.Fatalf("%d of 34 attempts were limited, want 4", limited)
	}
	// Variants of the address share the budget.
	if r := c.call("POST", "/api/v1/auth/login", map[string]string{"email": "  VICTIM@example.com ", "password": "another-guess"}); r.Status != 429 {
		t.Fatalf("case/whitespace variants must not dodge the limit: %d", r.Status)
	}
	if r := c.call("POST", "/api/v1/auth/login", map[string]string{"email": "other@example.com", "password": "x-password-123"}); r.Status != 401 {
		t.Fatalf("another account is unaffected: %d", r.Status)
	}
}

func TestForgotPasswordCannotBeUsedToMailBombAnAddress(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{shared: true, emailPerH: 3})
	owner, _ := e.register(t, "target@example.com")
	_ = owner
	c := e.newClient(t)
	before := len(e.mail.Messages())
	var codes []int
	for range 6 {
		codes = append(codes, c.call("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "target@example.com"}).Status)
	}
	for _, s := range codes {
		if s != 202 {
			t.Fatalf("the response must not reveal the limit (always 202): %v", codes)
		}
	}
	resets := 0
	for _, m := range e.mail.Messages()[before:] {
		if strings.Contains(m.Subject, "Reset") {
			resets++
		}
	}
	if resets != 3 {
		t.Fatalf("%d reset emails were sent to one address in an hour, want 3", resets)
	}
	// Unknown addresses consume budget too, so the limit itself reveals nothing about which accounts exist.
	for range 5 {
		if c.call("POST", "/api/v1/auth/forgot-password", map[string]string{"email": "nobody@example.com"}).Status != 202 {
			t.Fatal("unknown address must also get 202")
		}
	}
}

func TestVerificationAndAlertConfirmationEmailsAreCapped(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{shared: true, emailPerH: 2})
	c, _ := e.register(t, "owner@example.com")
	// resend-verification: 2 per hour, then 429 with Retry-After.
	got := []int{}
	for range 4 {
		got = append(got, c.call("POST", "/api/v1/auth/resend-verification", nil).Status)
	}
	if got[0] != 202 || got[1] != 202 || got[2] != 429 || got[3] != 429 {
		t.Fatalf("%v", got)
	}
	e.verify(t, c, "owner@example.com")
	// Alert recipient confirmations: a given external address can only be targeted twice an hour.
	n := 0
	for i := 0; i < 4; i++ {
		r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "victim@example.net"})
		if r.Status == 201 || r.Status == 409 {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d attempts against one external address got through, want 2", n)
	}
}

// ---- response hardening

func TestSecurityHeadersAndNoCrossOriginAccess(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{hsts: true})
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/plans", nil)
	req.Header.Set("Origin", "https://evil.example.net")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	h := res.Header
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'", "Strict-Transport-Security": "max-age=63072000; includeSubDomains",
	} {
		if h.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, h.Get(k), want)
		}
	}
	// Same-origin deployment: no CORS headers are ever sent, so browsers refuse cross-origin reads.
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("unexpected CORS header %s", k)
		}
	}
	// A cross-origin preflight must not be approved either.
	pre, _ := http.NewRequest("OPTIONS", e.srv.URL+"/api/v1/monitors", nil)
	pre.Header.Set("Origin", "https://evil.example.net")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pres, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}
	pres.Body.Close()
	if pres.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("preflight approved a foreign origin")
	}
}

func TestHSTSOnlyWhenEnabled(t *testing.T) {
	e := newEnv(t, 1000)
	res, _ := http.Get(e.srv.URL + "/healthz")
	res.Body.Close()
	if res.Header.Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS must not be sent by a plain-HTTP development server")
	}
}

// ---- observability

func TestRequestMetricsUseRouteTemplates(t *testing.T) {
	m := metrics.NewAPI()
	e := newEnvOpts(t, 1000, envOpts{metrics: m})
	c, _ := e.register(t, "m@example.com")
	id := c.call("POST", "/api/v1/monitors", monitorBody("x")).Body["monitor"].(map[string]any)["id"].(string)
	c.call("GET", "/api/v1/monitors/"+id, nil)
	c.call("GET", "/api/v1/monitors/"+id, nil)
	http.Get(e.srv.URL + "/definitely/not/a/route")

	fams, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]float64{}
	var inflight float64 = -1
	for _, f := range fams {
		for _, mm := range f.GetMetric() {
			switch f.GetName() {
			case "hawkeye_http_requests_total":
				labels := map[string]string{}
				for _, l := range mm.GetLabel() {
					labels[l.GetName()] = l.GetValue()
				}
				counts[labels["method"]+" "+labels["route"]+" "+labels["code"]] += mm.GetCounter().GetValue()
			case "hawkeye_http_requests_in_flight":
				inflight = mm.GetGauge().GetValue()
			}
		}
	}
	if counts["GET /api/v1/monitors/:monitorID 200"] != 2 {
		t.Fatalf("a route TEMPLATE (not the raw id URL) must be the label: %v", counts)
	}
	if counts["POST /api/v1/monitors 201"] != 1 || counts["GET unmatched 404"] != 1 {
		t.Fatalf("%v", counts)
	}
	for k := range counts {
		if strings.Contains(k, id) || strings.Contains(k, "definitely") {
			t.Fatalf("raw URL leaked into a label (cardinality bomb): %s", k)
		}
	}
	if inflight != 0 {
		t.Fatalf("in-flight gauge = %v after all requests finished", inflight)
	}
}

type captureReporter struct {
	mu   sync.Mutex
	errs []string
	tags []map[string]string
}

func (r *captureReporter) Capture(_ context.Context, err error, f map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err.Error())
	r.tags = append(r.tags, f)
}

func TestPanicsAreContainedCountedAndReported(t *testing.T) {
	m := metrics.NewAPI()
	rep := &captureReporter{}
	e := newEnvOpts(t, 1000, envOpts{metrics: m, reporter: rep})
	e.app.GET("/__test/panic", func(echo.Context) error { panic("boom: secret internal detail") })
	e.app.GET("/__test/fail", func(echo.Context) error { return errors.New("database exploded: password=hunter2") })

	res, err := http.Get(e.srv.URL + "/__test/panic")
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	body.Write(buf[:n])
	res.Body.Close()
	if res.StatusCode != 500 || !strings.Contains(body.String(), "INTERNAL_ERROR") || strings.Contains(body.String(), "boom") || strings.Contains(body.String(), "secret") {
		t.Fatalf("a panic must produce a generic 500 without details: %d %s", res.StatusCode, body.String())
	}
	fail, err := http.Get(e.srv.URL + "/__test/fail")
	if err != nil || fail.StatusCode != 500 {
		t.Fatalf("unexpected errors are 500s too: %v", err)
	}
	fb := make([]byte, 2048)
	fn, _ := fail.Body.Read(fb)
	fail.Body.Close()
	if strings.Contains(string(fb[:fn]), "hunter2") || strings.Contains(string(fb[:fn]), "database exploded") {
		t.Fatal("internal error text leaked to the client")
	}
	// The server survived and still works.
	if r, _ := http.Get(e.srv.URL + "/healthz"); r.StatusCode != 200 {
		t.Fatal("server unhealthy after a panic")
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.errs) != 2 { // exactly one report per failure: the panic is not reported again by the generic error handler
		t.Fatalf("want 2 reports, got %d: %v", len(rep.errs), rep.errs)
	}
	if !strings.Contains(rep.errs[0], "panic") || rep.tags[0]["request_id"] == "" || rep.tags[0]["kind"] != "panic" {
		t.Fatalf("reported: %v %v", rep.errs, rep.tags)
	}
	fams, _ := m.Registry.Gather()
	var panics float64
	for _, f := range fams {
		if f.GetName() == "hawkeye_http_panics_total" {
			panics = f.GetMetric()[0].GetCounter().GetValue()
		}
	}
	if panics != 1 {
		t.Fatalf("panic counter = %v", panics)
	}
}

func TestDatabasePoolMetricsAreExposed(t *testing.T) {
	m := metrics.NewAPI()
	e := newEnv(t, 1000)
	metrics.RegisterPool(m.Registry, e.pool)
	fams, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range fams {
		found[f.GetName()] = true
	}
	for _, name := range []string{"hawkeye_db_pool_max_connections", "hawkeye_db_pool_acquired_connections", "hawkeye_db_pool_empty_acquires_total", "hawkeye_db_pool_acquire_wait_seconds_total"} {
		if !found[name] {
			t.Errorf("metric %s missing", name)
		}
	}
}

func TestGlobalPerIPCeilingAbsorbsFloods(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{global: 60}) // burst = max(20, 10) = 20
	var limited int
	for i := 0; i < 60; i++ {
		res, err := http.Get(e.srv.URL + "/api/v1/plans")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == 429 {
			limited++
			if res.Header.Get("Retry-After") == "" {
				t.Fatal("missing Retry-After")
			}
		}
	}
	if limited == 0 {
		t.Fatal("a sustained flood from one IP was never limited")
	}
}

func TestUnknownAPIPathsAre404ForEveryone(t *testing.T) {
	e := newEnv(t, 1000)
	anon := e.newClient(t)
	authed, _ := e.register(t, "u@example.com")
	for _, c := range []*client{anon, authed} {
		for _, p := range []string{"/api/v1/nope", "/api/v1/metrics", "/api/v1/foo/bar", "/api/v1/auth/nope"} {
			if r := c.call("GET", p, nil); r.Status != 404 || errCode(r) != "NOT_FOUND" {
				t.Errorf("GET %s: %d %v", p, r.Status, r.Body)
			}
		}
	}
	// Real routes keep their protection.
	if r := anon.call("GET", "/api/v1/monitors", nil); r.Status != 401 {
		t.Fatalf("a real protected route must still be 401: %d", r.Status)
	}
}
