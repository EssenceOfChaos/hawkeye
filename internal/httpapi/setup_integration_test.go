package httpapi_test

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"hawkeye/internal/discovery"
	"hawkeye/internal/safehttp"
)

type noMX struct{}

func (noMX) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// setupService sends every request, for any host name, to one local TLS server.
func setupService(t *testing.T, resolvedTo string, h http.Handler) *discovery.Service {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return discovery.New(safehttp.Config{
		RootCAs:  pool,
		Resolver: staticResolver{resolvedTo},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}).WithMXResolver(noMX{})
}

func siteWithHealth() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" { // httptest's certificate also covers *.example.com: only the site itself exists here, not api.example.com
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<h1>hi</h1>"))
		case "/healthz":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	})
}

func TestMonitorSuggestionsMarkWhatIsAlreadyMonitored(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{setup: setupService(t, "93.184.216.34", siteWithHealth())})
	c, _ := e.register(t, "setup@example.com")

	r := c.call("POST", "/api/v1/monitors/suggestions", map[string]string{"domain": "Example.com"})
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	got := r.Body["suggestions"].([]any)
	if len(got) != 2 {
		t.Fatalf("want the home page and its health endpoint: %v", got)
	}
	for _, s := range got {
		if s.(map[string]any)["already_monitored"] != false {
			t.Fatalf("nothing is monitored yet: %v", s)
		}
	}

	// The person adds the suggested home page, as the app does, with a slightly different spelling of its address.
	home := got[0].(map[string]any)["monitor"].(map[string]any)
	if home["url"] != "https://example.com/" {
		t.Fatalf("%v", home)
	}
	if cr := c.call("POST", "/api/v1/monitors", map[string]any{"name": home["name"], "url": "https://EXAMPLE.com"}); cr.Status != 201 {
		t.Fatalf("%d %v", cr.Status, cr.Body)
	}
	r = c.call("POST", "/api/v1/monitors/suggestions", map[string]string{"domain": "example.com"})
	got = r.Body["suggestions"].([]any)
	if got[0].(map[string]any)["already_monitored"] != true || got[1].(map[string]any)["already_monitored"] != false {
		t.Fatalf("%v", got)
	}

	// Another organization asking about the same domain is answered from the cache, without this organization's monitors.
	other, _ := e.register(t, "other@example.com")
	r = other.call("POST", "/api/v1/monitors/suggestions", map[string]string{"domain": "example.com"})
	if r.Body["suggestions"].([]any)[0].(map[string]any)["already_monitored"] != false {
		t.Fatalf("one organization's monitors leaked into another's suggestions: %v", r.Body)
	}
}

func TestMonitorSuggestionsExplainWhyNot(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{setup: setupService(t, "10.0.0.5", siteWithHealth())})
	c, _ := e.register(t, "setup@example.com")
	for _, tc := range []struct {
		domain, code string
		status       int
	}{
		{"", "INVALID_HOST", 400},
		{"localhost", "INVALID_HOST", 400},
		{"example.com", "HOST_NOT_ALLOWED", 422}, // resolves to a private address
	} {
		r := c.call("POST", "/api/v1/monitors/suggestions", map[string]string{"domain": tc.domain})
		if r.Status != tc.status || errCode(r) != tc.code {
			t.Errorf("%q: %d %v", tc.domain, r.Status, r.Body)
		}
	}
}

func TestMonitorSuggestionsNeedSignIn(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{setup: setupService(t, "93.184.216.34", siteWithHealth())})
	r := e.newClient(t).call("POST", "/api/v1/monitors/suggestions", map[string]string{"domain": "example.com"})
	if r.Status != 401 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestSuggestMonitorsToolWorksWithAReadKey(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{setup: setupService(t, "93.184.216.34", siteWithHealth())})
	owner, _ := e.register(t, "setup@example.com")
	e.markVerified(t, "setup@example.com")
	_, token := newKey(t, owner, map[string]any{"name": "Claude", "scope": "read"})
	s := e.mcpSession(t, token)

	out := mustOK(t, callTool(t, s, "suggest_monitors", map[string]any{"domain": "example.com"}), "suggest")
	got := out["suggestions"].([]any)
	if len(got) != 2 {
		t.Fatalf("%v", out)
	}
	cw := got[1].(map[string]any)["create_with"].(map[string]any)
	if cw["type"] != "website" || cw["url"] != "https://example.com/healthz" || cw["name"] != "example.com health" {
		t.Fatalf("create_with must be ready for create_monitor: %v", cw)
	}
	if r := callTool(t, s, "suggest_monitors", map[string]any{"domain": "localhost"}); !r.IsError {
		t.Fatalf("%+v", r)
	}
}
