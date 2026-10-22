package sitecheck

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"hawkeye/internal/checks"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/toolguard"
)

func intp(i int) *int { return &i }

// fakeChecker answers per URL and records what it was asked.
type fakeChecker struct {
	mu      sync.Mutex
	answers map[string]checks.Result
	err     error
	asked   []checks.Target
}

func (f *fakeChecker) Check(_ context.Context, t checks.Target) (checks.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, t)
	if f.err != nil {
		return checks.Result{}, f.err
	}
	if r, ok := f.answers[t.URL]; ok {
		return r, nil
	}
	return failed(checks.CatOther), nil // an address the test did not expect
}

func (f *fakeChecker) urls() (out []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.asked {
		out = append(out, t.URL)
	}
	return out
}

func svc(f *fakeChecker) *Service {
	return &Service{checker: f, now: func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }, guard: toolguard.New[Report](toolguard.Options{})}
}

func ok(code, ms int) checks.Result {
	return checks.Result{Success: true, StatusCode: intp(code), ResponseTimeMs: intp(ms)}
}
func httpErr(code, ms int) checks.Result {
	return checks.Result{StatusCode: intp(code), ResponseTimeMs: intp(ms), ErrorCategory: checks.CatHTTPStatus}
}
func failed(cat string) checks.Result { return checks.Result{ErrorCategory: cat} }

func TestVerdicts(t *testing.T) {
	cases := []struct {
		name            string
		res             checks.Result
		verdict, reason string
		speed           string
	}{
		{"up and fast", ok(200, 120), VerdictUp, ReasonOK, SpeedFast},
		{"up, moderate", ok(200, 1200), VerdictUp, ReasonOK, SpeedModerate},
		{"up but slow", ok(200, 2600), VerdictUp, ReasonOK, SpeedSlow},
		{"boundary: 800 ms is still fast", ok(200, 800), VerdictUp, ReasonOK, SpeedFast},
		{"boundary: 1800 ms is still moderate", ok(200, 1800), VerdictUp, ReasonOK, SpeedModerate},
		{"not found is reachable", httpErr(404, 90), VerdictErrorResponse, ReasonHTTP4xx, SpeedFast},
		{"forbidden to bots is reachable", httpErr(403, 90), VerdictErrorResponse, ReasonHTTP4xx, SpeedFast},
		{"server error is down", httpErr(503, 300), VerdictDown, ReasonHTTP5xx, SpeedFast},
		{"dns", failed(checks.CatDNS), VerdictDown, ReasonDNS, ""},
		{"connect", failed(checks.CatConnect), VerdictDown, ReasonConnect, ""},
		{"tls", failed(checks.CatTLS), VerdictDown, ReasonTLS, ""},
		{"timeout", failed(checks.CatTimeout), VerdictDown, ReasonTimeout, ""},
		{"redirect loop", failed(checks.CatRedirect), VerdictDown, ReasonRedirect, ""},
		{"anything else", failed(checks.CatOther), VerdictDown, ReasonOther, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeChecker{answers: map[string]checks.Result{"https://example.com/": tc.res}}
			r, err := svc(f).Inspect(context.Background(), "https://example.com")
			if err != nil {
				t.Fatal(err)
			}
			if r.Verdict != tc.verdict || r.Reason != tc.reason || r.Speed != tc.speed {
				t.Errorf("%+v", r)
			}
			if (tc.res.StatusCode == nil) != (r.StatusCode == nil) || (tc.res.ResponseTimeMs == nil) != (r.ResponseTimeMs == nil) {
				t.Errorf("status and timing must be reported exactly when a response arrived: %+v", r)
			}
		})
	}
}

func TestOnlyTheHostIsUsedAndTheRequestIsIdentified(t *testing.T) {
	f := &fakeChecker{answers: map[string]checks.Result{"https://example.com/": ok(200, 50)}}
	r, err := svc(f).Inspect(context.Background(), "https://Example.com/reset?token=SECRET#x")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.urls(); len(got) != 1 || got[0] != "https://example.com/" {
		t.Fatalf("requested %v: the path and query must never be sent", got)
	}
	if r.URL != "https://example.com/" {
		t.Errorf("URL = %q", r.URL)
	}
	if f.asked[0].Method != "GET" || f.asked[0].UserAgent != UserAgent || len(f.asked[0].Headers) != 0 || f.asked[0].Body != "" {
		t.Errorf("%+v", f.asked[0])
	}
}

func TestBareDomainTriesHTTPSThenFallsBackToHTTP(t *testing.T) {
	f := &fakeChecker{answers: map[string]checks.Result{
		"https://example.com/": failed(checks.CatTLS),
		"http://example.com/":  ok(200, 70),
	}}
	r, err := svc(f).Inspect(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.urls(); len(got) != 2 || got[0] != "https://example.com/" || got[1] != "http://example.com/" {
		t.Errorf("requests = %v", got)
	}
	if r.Verdict != VerdictUp || r.URL != "http://example.com/" || r.HTTPSError != ReasonTLS {
		t.Errorf("%+v", r)
	}
}

func TestNoFallbackWhenHTTPSAnsweredOrFailedForOtherReasons(t *testing.T) {
	for name, res := range map[string]checks.Result{
		"answered 200":  ok(200, 10),
		"answered 500":  httpErr(500, 10),
		"dns":           failed(checks.CatDNS),
		"timeout":       failed(checks.CatTimeout),
		"redirect loop": failed(checks.CatRedirect),
	} {
		f := &fakeChecker{answers: map[string]checks.Result{"https://example.com/": res}}
		if _, err := svc(f).Inspect(context.Background(), "example.com"); err != nil {
			t.Fatal(err)
		}
		if got := f.urls(); len(got) != 1 {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestWhenBothFailTheHTTPSFailureIsReported(t *testing.T) {
	f := &fakeChecker{answers: map[string]checks.Result{
		"https://example.com/": failed(checks.CatConnect),
		"http://example.com/":  failed(checks.CatTimeout),
	}}
	r, _ := svc(f).Inspect(context.Background(), "example.com")
	if r.Reason != ReasonConnect || r.URL != "https://example.com/" || r.HTTPSError != "" {
		t.Errorf("%+v", r)
	}
}

func TestAnExplicitSchemeOrPortIsHonouredWithoutFallback(t *testing.T) {
	for in, want := range map[string]string{
		"http://example.com":  "http://example.com/",
		"https://example.com": "https://example.com/",
		"example.com:80":      "http://example.com/",
		"example.com:443":     "https://example.com/",
	} {
		f := &fakeChecker{answers: map[string]checks.Result{want: failed(checks.CatConnect)}}
		r, err := svc(f).Inspect(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.urls(); len(got) != 1 || got[0] != want || r.Reason != ReasonConnect {
			t.Errorf("%q: requested %v, %+v", in, got, r)
		}
	}
}

func TestFinalURLIsShownOnlyWhenItDiffers(t *testing.T) {
	r := checks.Result{Success: true, StatusCode: intp(200), ResponseTimeMs: intp(10), FinalURL: "https://www.example.com/home"}
	f := &fakeChecker{answers: map[string]checks.Result{"https://example.com/": r}}
	got, _ := svc(f).Inspect(context.Background(), "example.com")
	if got.FinalURL != "https://www.example.com/home" {
		t.Errorf("%+v", got)
	}
	r.FinalURL = "https://example.com/"
	f = &fakeChecker{answers: map[string]checks.Result{"https://example.com/": r}}
	if got, _ := svc(f).Inspect(context.Background(), "example.com"); got.FinalURL != "" {
		t.Errorf("%+v", got)
	}
}

func TestALongRedirectAddressIsShortened(t *testing.T) {
	long := "https://www.example.com/" + strings.Repeat("a", 500)
	r := checks.Result{Success: true, StatusCode: intp(200), ResponseTimeMs: intp(10), FinalURL: long}
	f := &fakeChecker{answers: map[string]checks.Result{"https://example.com/": r}}
	got, _ := svc(f).Inspect(context.Background(), "example.com")
	if n := len([]rune(got.FinalURL)); n != 121 || !strings.HasSuffix(got.FinalURL, "…") {
		t.Errorf("%d characters: %q", n, got.FinalURL)
	}
}

func TestInvalidInputNeverReachesTheChecker(t *testing.T) {
	f := &fakeChecker{}
	for _, in := range []string{"", "127.0.0.1", "localhost", "10.0.0.1", "example.com:8080", "user@example.com", "ftp://example.com", "intranet"} {
		var ih *InvalidHostError
		if _, err := svc(f).Inspect(context.Background(), in); !errors.As(err, &ih) {
			t.Errorf("%q: err = %v", in, err)
		}
	}
	if len(f.urls()) != 0 {
		t.Errorf("requests were made: %v", f.urls())
	}
}

func TestBlockedAndCancelledAreErrorsNotVerdicts(t *testing.T) {
	f := &fakeChecker{answers: map[string]checks.Result{"https://example.com/": failed(checks.CatBlocked)}}
	if _, err := svc(f).Inspect(context.Background(), "example.com"); !errors.Is(err, ErrBlocked) {
		t.Errorf("err = %v", err)
	}
	f = &fakeChecker{err: context.Canceled}
	if _, err := svc(f).Inspect(context.Background(), "example.com"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestSharedLimitsApply(t *testing.T) {
	// A check that errors is not cached but still spends the host's budget.
	f := &fakeChecker{err: errors.New("boom")}
	s := svc(f)
	s.guard = toolguard.New[Report](toolguard.Options{TargetMax: 2})
	for i := 0; i < 2; i++ {
		_, _ = s.Inspect(context.Background(), "example.com")
	}
	var tl *TargetLimitedError
	if _, err := s.Inspect(context.Background(), "https://example.com/other"); !errors.As(err, &tl) {
		t.Errorf("err = %v", err)
	}
	if len(f.urls()) != 2 {
		t.Errorf("a limited request still reached the checker: %v", f.urls())
	}
	// An answer is cached (down or up) and costs no budget.
	f2 := &fakeChecker{answers: map[string]checks.Result{"https://up.example/": ok(200, 5)}}
	s2 := svc(f2)
	for i := 0; i < 20; i++ {
		if _, err := s2.Inspect(context.Background(), "up.example"); err != nil {
			t.Fatal(err)
		}
	}
	if len(f2.urls()) != 1 {
		t.Errorf("%d requests for 20 asks", len(f2.urls()))
	}
}

// --- the real checker, over real connections to local servers, with the production address and port policy ---

type resolver string

func (r resolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr(string(r))}, nil
}

func realService(t *testing.T, resolvedTo string, h http.Handler) *Service {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return New(safehttp.Config{
		RootCAs:  pool,
		Resolver: resolver(resolvedTo),
		Dial: func(ctx context.Context, n, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, n, srv.Listener.Addr().String())
		},
	})
}

func TestRealRequestsReportStatusTimingAndRedirects(t *testing.T) {
	var gotUA, gotPath, gotQuery string
	s := realService(t, "93.184.216.34", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotPath, gotQuery = r.Header.Get("User-Agent"), r.URL.Path, r.URL.RawQuery
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/welcome?session=abc", http.StatusMovedPermanently)
			return
		}
		_, _ = w.Write([]byte("hello"))
	}))
	r, err := s.Inspect(context.Background(), "https://example.com/private/path?token=SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != VerdictUp || r.StatusCode == nil || *r.StatusCode != 200 || r.ResponseTimeMs == nil || r.Speed != SpeedFast {
		t.Fatalf("%+v", r)
	}
	if r.FinalURL != "https://example.com/welcome" {
		t.Errorf("FinalURL = %q (the query must be dropped)", r.FinalURL)
	}
	if gotUA != UserAgent || gotPath != "/welcome" || gotQuery != "session=abc" {
		t.Errorf("server saw ua=%q path=%q query=%q", gotUA, gotPath, gotQuery)
	}
}

func TestRealServerErrorsAreVerdicts(t *testing.T) {
	for code, want := range map[int]string{404: VerdictErrorResponse, 403: VerdictErrorResponse, 500: VerdictDown, 503: VerdictDown} {
		s := realService(t, "93.184.216.34", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		r, err := s.Inspect(context.Background(), "example.com")
		if err != nil || r.Verdict != want || r.StatusCode == nil || *r.StatusCode != code {
			t.Errorf("%d: %+v, %v", code, r, err)
		}
	}
}

func TestRealRefusalOfPrivateDestinations(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.5", "169.254.169.254", "192.168.1.1", "::1"} {
		s := realService(t, ip, http.NotFoundHandler())
		if _, err := s.Inspect(context.Background(), "looks-public.example.com"); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: err = %v, want ErrBlocked", ip, err)
		}
	}
}

func TestRealRedirectToAPrivateAddressIsRefused(t *testing.T) {
	// The first hop is public; the redirect target resolves to an internal address, so the second connection must be refused.
	var calls int
	var mu sync.Mutex
	res := &switchResolver{answers: map[string]string{"example.com": "93.184.216.34", "internal.example.org": "10.0.0.5"}}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Redirect(w, r, "https://internal.example.org/admin", http.StatusFound)
	}))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	s := New(safehttp.Config{RootCAs: pool, Resolver: res,
		Dial: func(ctx context.Context, n, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, n, srv.Listener.Addr().String())
		}})
	_, err := s.Inspect(context.Background(), "example.com")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if calls != 1 {
		t.Errorf("the server was contacted %d times; the redirect target must never be", calls)
	}
}

type switchResolver struct{ answers map[string]string }

func (r *switchResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if ip, ok := r.answers[host]; ok {
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}
