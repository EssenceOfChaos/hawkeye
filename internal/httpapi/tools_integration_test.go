package httpapi_test

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"hawkeye/internal/safehttp"
	"hawkeye/internal/sitecheck"
	"hawkeye/internal/sslcheck"
)

type staticResolver []string

func (r staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, s := range r {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

// sslService returns a checker whose connections all go to a local TLS server (httptest's certificate is valid for example.com), with the
// production address and port policy: the host name resolves to resolvedTo, which must be public for the connection to be allowed.
func sslService(t *testing.T, resolvedTo string) *sslcheck.Service {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return sslcheck.New(safehttp.Config{
		RootCAs:  pool,
		Resolver: staticResolver{resolvedTo},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	})
}

func TestSSLCheckReturnsTheCertificateReport(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{ssl: sslService(t, "93.184.216.34")})
	c := e.newClient(t)
	r := c.call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "https://Example.com/pricing"})
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r.Body["host"] != "example.com" || r.Body["trusted"] != true || r.Body["hostname_match"] != true {
		t.Fatalf("%v", r.Body)
	}
	cert, ok := r.Body["certificate"].(map[string]any)
	if !ok || cert["not_after"] == nil || cert["sha256_fingerprint"] == nil {
		t.Fatalf("certificate = %v", r.Body["certificate"])
	}
	if _, ok := r.Body["problems"].([]any); !ok {
		t.Fatalf("problems must always be a list: %v", r.Body["problems"])
	}
}

func TestSSLCheckAnswersWhyItCannotCheck(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{ssl: sslService(t, "93.184.216.34")})
	c := e.newClient(t)
	for _, tc := range []struct {
		host, code string
		status     int
	}{
		{"", "INVALID_HOST", 400},
		{"127.0.0.1", "INVALID_HOST", 400},
		{"localhost", "INVALID_HOST", 400},
		{"example.com:8080", "INVALID_HOST", 400},
		{"https://user:pw@example.com", "INVALID_HOST", 400},
	} {
		r := c.call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": tc.host})
		if r.Status != tc.status || errCode(r) != tc.code {
			t.Errorf("%q: %d %v", tc.host, r.Status, r.Body)
		}
	}
}

func TestSSLCheckRefusesHostsThatResolveToPrivateAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "192.168.0.1"} {
		e := newEnvOpts(t, 1000, envOpts{ssl: sslService(t, ip)})
		r := e.newClient(t).call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "internal-looking.example.com"})
		if r.Status != 422 || errCode(r) != "HOST_NOT_ALLOWED" {
			t.Errorf("%s: %d %v", ip, r.Status, r.Body)
		}
		if msg, _ := r.Body["error"].(map[string]any)["message"].(string); strings.Contains(msg, ip) {
			t.Errorf("the message must not reveal the address it resolved to: %q", msg)
		}
	}
}

func TestSSLCheckNeedsTheCSRFTokenAndIsOffWithoutAService(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{ssl: sslService(t, "93.184.216.34")})
	c := e.newClient(t)
	c.do("GET", "/api/v1/auth/me", nil, false)
	if r := c.do("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "example.com"}, false); r.Status != 403 {
		t.Errorf("without the CSRF header: %d %v", r.Status, r.Body)
	}
	if r := c.do("GET", "/api/v1/tools/ssl-check?host=example.com", nil, false); r.Status == 200 {
		t.Errorf("a GET must never trigger a check: %d", r.Status)
	}

	off := newEnv(t, 1000)
	if r := off.newClient(t).call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "example.com"}); r.Status != 503 || errCode(r) != "TOOL_UNAVAILABLE" {
		t.Errorf("%d %v", r.Status, r.Body)
	}
}

func TestSSLCheckIsLimitedPerClient(t *testing.T) {
	e := newEnvOpts(t, 3, envOpts{ssl: sslService(t, "93.184.216.34")})
	c := e.newClient(t)
	var last resp
	for i := 0; i < 4; i++ {
		last = c.call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "example.com"})
	}
	if last.Status != 429 || errCode(last) != "RATE_LIMITED" {
		t.Errorf("fourth request in a minute: %d %v", last.Status, last.Body)
	}
}

func TestSSLCheckHourlyCapIsSharedAcrossProcesses(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{shared: true, ssl: sslService(t, "93.184.216.34")})
	c := e.newClient(t)
	var status int
	for i := 0; i < 41; i++ {
		status = c.call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "example.com"}).Status
	}
	if status != 429 {
		t.Errorf("41st request in an hour: %d", status)
	}
}

func TestSSLCheckStoresNothingAboutTheHostsAskedAbout(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{shared: true, ssl: sslService(t, "93.184.216.34")})
	e.newClient(t).call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "very-specific-host-name.example.com"})
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM rate_limits WHERE key LIKE '%very-specific%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rate-limit rows mention the host: %d", n)
	}
}

// siteService returns a website checker whose connections all go to a local TLS server running h (its certificate is valid for example.com).
func siteService(t *testing.T, resolvedTo string, h http.Handler) *sitecheck.Service {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return sitecheck.New(safehttp.Config{
		RootCAs:  pool,
		Resolver: staticResolver{resolvedTo},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	})
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) })
}

func TestSiteCheckReportsWhetherTheSiteAnswers(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{site: siteService(t, "93.184.216.34", okHandler())})
	r := e.newClient(t).call("POST", "/api/v1/tools/site-check", map[string]string{"host": "https://Example.com/some/page?token=x"})
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r.Body["host"] != "example.com" || r.Body["url"] != "https://example.com/" || r.Body["verdict"] != "up" || r.Body["reason"] != "ok" ||
		r.Body["status_code"] != float64(200) || r.Body["response_time_ms"] == nil || r.Body["speed"] == nil {
		t.Fatalf("%v", r.Body)
	}

	down := newEnvOpts(t, 1000, envOpts{site: siteService(t, "93.184.216.34", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))})
	r = down.newClient(t).call("POST", "/api/v1/tools/site-check", map[string]string{"host": "example.com"})
	if r.Status != 200 || r.Body["verdict"] != "down" || r.Body["reason"] != "http_5xx" || r.Body["status_code"] != float64(503) {
		t.Fatalf("a 503 is an answer about the site, not an API error: %d %v", r.Status, r.Body)
	}
}

func TestSiteCheckRefusesWhatItShouldNotTouch(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{site: siteService(t, "93.184.216.34", okHandler())})
	c := e.newClient(t)
	for _, host := range []string{"", "127.0.0.1", "localhost", "example.com:8080", "https://user:pw@example.com", "ftp://example.com"} {
		if r := c.call("POST", "/api/v1/tools/site-check", map[string]string{"host": host}); r.Status != 400 || errCode(r) != "INVALID_HOST" {
			t.Errorf("%q: %d %v", host, r.Status, r.Body)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "192.168.0.1"} {
		blocked := newEnvOpts(t, 1000, envOpts{site: siteService(t, ip, okHandler())})
		r := blocked.newClient(t).call("POST", "/api/v1/tools/site-check", map[string]string{"host": "internal-looking.example.com"})
		if r.Status != 422 || errCode(r) != "HOST_NOT_ALLOWED" {
			t.Errorf("%s: %d %v", ip, r.Status, r.Body)
		}
	}
}

func TestSiteCheckNeedsCSRFIsPostOnlyAndOffWithoutAService(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{site: siteService(t, "93.184.216.34", okHandler())})
	c := e.newClient(t)
	c.do("GET", "/api/v1/auth/me", nil, false)
	if r := c.do("POST", "/api/v1/tools/site-check", map[string]string{"host": "example.com"}, false); r.Status != 403 {
		t.Errorf("without the CSRF header: %d %v", r.Status, r.Body)
	}
	if r := c.do("GET", "/api/v1/tools/site-check?host=example.com", nil, false); r.Status == 200 {
		t.Errorf("a GET must never trigger a request to a third party: %d", r.Status)
	}
	off := newEnv(t, 1000)
	if r := off.newClient(t).call("POST", "/api/v1/tools/site-check", map[string]string{"host": "example.com"}); r.Status != 503 || errCode(r) != "TOOL_UNAVAILABLE" {
		t.Errorf("%d %v", r.Status, r.Body)
	}
}

func TestSiteCheckIsLimitedPerClientAndPerHour(t *testing.T) {
	e := newEnvOpts(t, 3, envOpts{site: siteService(t, "93.184.216.34", okHandler())})
	c := e.newClient(t)
	var last resp
	for i := 0; i < 4; i++ {
		last = c.call("POST", "/api/v1/tools/site-check", map[string]string{"host": "example.com"})
	}
	if last.Status != 429 || errCode(last) != "RATE_LIMITED" {
		t.Errorf("fourth request in a minute: %d %v", last.Status, last.Body)
	}

	hourly := newEnvOpts(t, 1000, envOpts{shared: true, site: siteService(t, "93.184.216.34", okHandler())})
	c = hourly.newClient(t)
	var status int
	for i := 0; i < 41; i++ {
		status = c.call("POST", "/api/v1/tools/site-check", map[string]string{"host": "example.com"}).Status
	}
	if status != 429 {
		t.Errorf("41st request in an hour: %d", status)
	}
}

func TestEachToolHasItsOwnPerClientBudget(t *testing.T) {
	e := newEnvOpts(t, 2, envOpts{shared: true, ssl: sslService(t, "93.184.216.34"), site: siteService(t, "93.184.216.34", okHandler())})
	c := e.newClient(t)
	for i := 0; i < 3; i++ {
		c.call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "example.com"})
	}
	if r := c.call("POST", "/api/v1/tools/ssl-check", map[string]string{"host": "example.com"}); r.Status != 429 {
		t.Fatalf("ssl-check should be limited: %d", r.Status)
	}
	if r := c.call("POST", "/api/v1/tools/site-check", map[string]string{"host": "example.com"}); r.Status != 200 {
		t.Errorf("using one tool must not use up the other's budget: %d %v", r.Status, r.Body)
	}
}

func TestSiteCheckStoresNothingAboutTheHostsAskedAbout(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{shared: true, site: siteService(t, "93.184.216.34", okHandler())})
	e.newClient(t).call("POST", "/api/v1/tools/site-check", map[string]string{"host": "very-specific-host-name.example.com"})
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM rate_limits WHERE key LIKE '%very-specific%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rate-limit rows mentioning the host: %d (%v)", n, err)
	}
}
