package checks

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hawkeye/internal/safehttp"
)

// loopbackCfg lets checks reach httptest servers on 127.0.0.1 while keeping every other rule: redirects
// are revalidated, and any other private address is still refused.
func loopbackCfg() safehttp.Config {
	return safehttp.Config{
		AllowIP:   func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") || safehttp.IsPublicIP(a) },
		AllowPort: func(int) bool { return true },
	}
}

type nxResolver struct{}

func (nxResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func intp(i int) *int { return &i }

func check(t *testing.T, cfg safehttp.Config, tg Target) Result {
	t.Helper()
	if tg.Method == "" {
		tg.Method = "GET"
	}
	if tg.Timeout == 0 {
		tg.Timeout = 2 * time.Second
	}
	res, err := NewChecker(cfg).Check(t.Context(), tg)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSuccessfulCheck(t *testing.T) {
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		w.Write([]byte(strings.Repeat("x", 1<<20))) // large body is drained in part, never kept
	}))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL})
	if !r.Success || r.StatusCode == nil || *r.StatusCode != 200 || r.ResponseTimeMs == nil || r.ErrorCategory != "" || r.ErrorSummary != "" {
		t.Fatalf("%+v", r)
	}
	if !strings.HasPrefix(ua.Load().(string), "Hawkeye-Monitor/") || r.FinishedAt.Before(r.CheckedAt) {
		t.Fatalf("ua=%v %+v", ua.Load(), r)
	}
}

func TestHTTPErrorStatusIsAnHTTPFailure(t *testing.T) {
	for _, code := range []int{400, 404, 500, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		r := check(t, loopbackCfg(), Target{URL: srv.URL})
		srv.Close()
		if r.Success || r.ErrorCategory != CatHTTPStatus || r.StatusCode == nil || *r.StatusCode != code || r.ResponseTimeMs == nil {
			t.Errorf("%d: %+v", code, r)
		}
	}
}

func TestExpectedStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }))
	defer srv.Close()
	if r := check(t, loopbackCfg(), Target{URL: srv.URL, ExpectedStatus: intp(404)}); !r.Success {
		t.Fatalf("404 expected and received: %+v", r)
	}
	r := check(t, loopbackCfg(), Target{URL: srv.URL, ExpectedStatus: intp(200)})
	if r.Success || !strings.Contains(r.ErrorSummary, "expected 200") {
		t.Fatalf("%+v", r)
	}
}

func TestHeadMethod(t *testing.T) {
	var method atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { method.Store(r.Method) }))
	defer srv.Close()
	check(t, loopbackCfg(), Target{URL: srv.URL, Method: "HEAD"})
	if method.Load() != "HEAD" {
		t.Fatalf("method %v", method.Load())
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	start := time.Now()
	r := check(t, loopbackCfg(), Target{URL: srv.URL, Timeout: 150 * time.Millisecond})
	if r.Success || r.ErrorCategory != CatTimeout || r.StatusCode != nil || r.ResponseTimeMs != nil {
		t.Fatalf("%+v", r)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("timeout not enforced: %v", time.Since(start))
	}
}

func TestDNSFailure(t *testing.T) {
	cfg := loopbackCfg()
	cfg.Resolver = nxResolver{}
	r := check(t, cfg, Target{URL: "http://no-such-host.example/"})
	if r.Success || r.ErrorCategory != CatDNS {
		t.Fatalf("%+v", r)
	}
}

func TestConnectionRefused(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	r := check(t, loopbackCfg(), Target{URL: "http://" + addr + "/"})
	if r.Success || r.ErrorCategory != CatConnect {
		t.Fatalf("%+v", r)
	}
}

func TestUntrustedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL})
	if r.Success || r.ErrorCategory != CatTLS {
		t.Fatalf("%+v", r)
	}
}

func TestRedirectsAreFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusMovedPermanently)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	if r := check(t, loopbackCfg(), Target{URL: srv.URL}); !r.Success || *r.StatusCode != 200 {
		t.Fatalf("%+v", r)
	}
}

func TestRedirectLoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", 302) }))
	defer srv.Close()
	if r := check(t, loopbackCfg(), Target{URL: srv.URL}); r.Success || r.ErrorCategory != CatRedirect {
		t.Fatalf("%+v", r)
	}
}

func TestRedirectToProhibitedDestinationIsBlocked(t *testing.T) {
	for _, loc := range []string{"http://169.254.169.254/latest/meta-data/", "http://10.0.0.1/", "http://[::1]:1/", "http://[::ffff:192.168.0.1]/"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, loc, 302) }))
		r := check(t, loopbackCfg(), Target{URL: srv.URL})
		srv.Close()
		if r.Success || r.ErrorCategory != CatBlocked {
			t.Errorf("%s: %+v", loc, r)
		}
	}
}

func TestProhibitedDestinationsUnderProductionPolicy(t *testing.T) {
	// Zero Config = the production policy.
	for _, u := range []string{"http://127.0.0.1/", "http://10.0.0.5/secret", "http://169.254.169.254/", "http://[::1]/", "http://localhost/"} {
		r := check(t, safehttp.Config{}, Target{URL: u})
		if r.Success || (r.ErrorCategory != CatBlocked && r.ErrorCategory != CatDNS) {
			t.Errorf("%s: %+v", u, r)
		}
		if strings.Contains(r.ErrorSummary, "10.0.0.5") || strings.Contains(r.ErrorSummary, "169.254") {
			t.Errorf("summary leaks the address: %q", r.ErrorSummary)
		}
	}
}

func TestCancelledCheckIsNotARecordableResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	_, err := NewChecker(loopbackCfg()).Check(ctx, Target{URL: srv.URL, Method: "GET", Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("cancellation must be reported as an error, not as a failed check")
	}
}

func TestCustomUserAgentAndFinalURL(t *testing.T) {
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/landing/page?token=secret#frag", http.StatusFound)
			return
		}
		ua.Store(r.Header.Get("User-Agent"))
	}))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL + "/", UserAgent: "HawkEye-StatusChecker/1.0 (+https://example.test)"})
	if ua.Load() != "HawkEye-StatusChecker/1.0 (+https://example.test)" {
		t.Errorf("user agent = %v", ua.Load())
	}
	// Where the request ended, without the query (which can hold tokens) or fragment.
	if r.FinalURL != srv.URL+"/landing/page" {
		t.Errorf("FinalURL = %q", r.FinalURL)
	}
	if r2 := check(t, loopbackCfg(), Target{URL: srv.URL + "/landing/page"}); r2.FinalURL != srv.URL+"/landing/page" {
		t.Errorf("a request with no redirect ends where it started: %q", r2.FinalURL)
	}
	if r3 := check(t, safehttp.Config{Resolver: nxResolver{}}, Target{URL: "https://nope.example/"}); r3.FinalURL != "" {
		t.Errorf("no response, no final URL: %q", r3.FinalURL)
	}
}
