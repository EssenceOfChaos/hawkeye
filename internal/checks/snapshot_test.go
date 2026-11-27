package checks

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The allowlist must never keep a header that can carry a credential or a session.
func TestHeaderAllowlistHasNoSecrets(t *testing.T) {
	for _, name := range []string{"Set-Cookie", "Cookie", "Authorization", "Proxy-Authorization", "X-Api-Key", "X-Auth-Token", "X-Amz-Security-Token",
		"X-Csrf-Token", "Api-Key", "X-Access-Token", "X-Session-Id", "Proxy-Authenticate"} {
		if keep, _ := allowedHeader(name); keep {
			t.Errorf("%s is on the snapshot allowlist", name)
		}
	}
	for _, h := range snapshotHeaders {
		lower := strings.ToLower(h.name)
		for _, bad := range []string{"cookie", "auth", "token", "key", "secret", "session", "csrf"} {
			if strings.Contains(lower, bad) && h.name != "Www-Authenticate" {
				t.Errorf("allowlisted %s looks like it can carry a credential", h.name)
			}
		}
	}
}

func TestKeepHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Server", "nginx")
	h.Set("Set-Cookie", "session=abc123")
	h.Set("X-Api-Key", "sk_live_123")
	h.Set("X-Custom-Token", "tok")
	h.Set("Location", "https://example.com/login?next=/admin&token=abc#frag")
	h.Set("WWW-Authenticate", `Bearer realm="internal-admin", error="invalid_token"`)
	h.Set("X-Powered-By", strings.Repeat("x", 500))
	got := keepHeaders(h)
	want := map[string]string{"Server": "nginx", "Location": "https://example.com/login", "Www-Authenticate": "Bearer"}
	if len(got) != 4 {
		t.Fatalf("kept %+v", got)
	}
	for _, kh := range got {
		if w, ok := want[kh.Name]; ok && kh.Value != w {
			t.Errorf("%s = %q, want %q", kh.Name, kh.Value, w)
		}
		if kh.Name == "X-Powered-By" && len([]rune(kh.Value)) != maxHeaderValueLen {
			t.Errorf("X-Powered-By kept %d characters", len([]rune(kh.Value)))
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Fatalf("not sorted: %+v", got)
		}
	}
}

func TestFactsOfAFailedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "cloudflare")
		w.Header().Set("Set-Cookie", "s=1")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<h1>Bad gateway</h1>"))
	}))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL})
	f := r.Facts
	if f == nil || f.StatusCode != 502 || f.ContentType != "text/html" || f.ServerAddr != "127.0.0.1" || f.SizeBytes != 20 || f.Timings == nil {
		t.Fatalf("facts %+v", f)
	}
	for _, h := range f.Headers {
		if h.Name == "Set-Cookie" {
			t.Fatal("Set-Cookie kept")
		}
	}
	if r.Excerpt != nil {
		t.Fatal("an excerpt without CaptureBody")
	}
}

// A check that never got a response keeps what it reached: the server address and the timings, no headers.
func TestFactsOfATimeout(t *testing.T) {
	srv := httptest.NewServer(sleepy(2 * time.Second))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL, Timeout: 200 * time.Millisecond, CaptureBody: true})
	f := r.Facts
	if r.Success || f == nil || f.StatusCode != 0 || f.ServerAddr != "127.0.0.1" || len(f.Headers) != 0 || f.Timings == nil || r.Excerpt != nil {
		t.Fatalf("facts %+v excerpt %+v", f, r.Excerpt)
	}
}

func TestExcerptHidesCredentialsAndIsBounded(t *testing.T) {
	body := "error: token sk_live_secret rejected for header-value-42 " + strings.Repeat("y", 10_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	tg := Target{URL: srv.URL, CaptureBody: true, Auth: Auth{Type: AuthBearer, Secret: "sk_live_secret"},
		Headers: []Header{{Name: "X-Tenant", Value: "header-value-42"}}}
	r := check(t, loopbackCfg(), tg)
	e := r.Excerpt
	if e == nil || !e.Truncated || e.Binary || len(e.Text) > MaxExcerptBytes+50 {
		t.Fatalf("excerpt %+v", e)
	}
	if strings.Contains(e.Text, "sk_live_secret") || strings.Contains(e.Text, "header-value-42") || !strings.Contains(e.Text, hidden) {
		t.Fatalf("not redacted: %q", e.Text[:80])
	}
	// The same check with assertions (the body is read for them) keeps the same excerpt.
	tg.Assertions = []Assertion{{Type: "contains", Value: "never"}}
	if r := check(t, loopbackCfg(), tg); r.Excerpt == nil || !strings.HasPrefix(r.Excerpt.Text, "error: token "+hidden) {
		t.Fatalf("with assertions: %+v", r.Excerpt)
	}
}

func TestExcerptOnlyForFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("all good")) }))
	defer srv.Close()
	if r := check(t, loopbackCfg(), Target{URL: srv.URL, CaptureBody: true}); !r.Success || r.Excerpt != nil {
		t.Fatalf("%+v", r.Excerpt)
	}
	// An assertion failure is a failure too.
	r := check(t, loopbackCfg(), Target{URL: srv.URL, CaptureBody: true, Assertions: []Assertion{{Type: "contains", Value: "missing"}}})
	if r.Success || r.Excerpt == nil || r.Excerpt.Text != "all good" || r.Excerpt.Truncated {
		t.Fatalf("%+v", r.Excerpt)
	}
}

func TestBinaryBodiesAreNotKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte{0xff, 0xfe, 0x00, 0x81})
	}))
	defer srv.Close()
	if r := check(t, loopbackCfg(), Target{URL: srv.URL, CaptureBody: true}); r.Excerpt == nil || !r.Excerpt.Binary || r.Excerpt.Text != "" {
		t.Fatalf("%+v", r.Excerpt)
	}
}

func TestWorkflowFactsAndExcerptHideSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"token":"tok-0123456789"}`))
			return
		}
		w.Header().Set("Location", "/again/tok-0123456789")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden for tok-0123456789 with pw-hunter2"))
	}))
	defer srv.Close()
	w := Workflow{CaptureBody: true, Secrets: map[string]string{"pw": "pw-hunter2"}, Steps: []Step{
		{Name: "log in", Target: Target{Method: "POST", URL: srv.URL + "/login", Body: `{"pw":"{{secret.pw}}"}`, Timeout: 2 * time.Second},
			Extractions: []Extraction{{Name: "token", From: ExtractJSONPath, Path: "token"}}},
		{Name: "account", Target: Target{Method: "GET", URL: srv.URL + "/account", Timeout: 2 * time.Second,
			Headers: []Header{{Name: "Authorization", Value: "Bearer {{token}}"}}}},
	}}
	res, err := NewChecker(loopbackCfg()).RunWorkflow(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.FailedStep != 1 || res.Facts == nil || res.Facts.StatusCode != 403 || res.Steps[0].Facts == nil {
		t.Fatalf("%+v", res)
	}
	for _, h := range res.Facts.Headers {
		if strings.Contains(h.Value, "tok-0123456789") {
			t.Fatalf("header %s not redacted: %s", h.Name, h.Value)
		}
	}
	if e := res.Excerpt; e == nil || strings.Contains(e.Text, "tok-0123456789") || strings.Contains(e.Text, "pw-hunter2") {
		t.Fatalf("excerpt %+v", res.Excerpt)
	}
}

func TestFingerprintIgnoresVolatileHeadersAndSmallSizeChanges(t *testing.T) {
	a := ResponseFacts{StatusCode: 200, ContentType: "application/json", SizeBytes: 40_000, ServerAddr: "203.0.113.7",
		Headers: []Header{{Name: "Age", Value: "12"}, {Name: "Server", Value: "nginx"}, {Name: "X-Request-Id", Value: "a1"}}}
	b := a
	b.Headers = []Header{{Name: "Age", Value: "300"}, {Name: "Server", Value: "nginx"}, {Name: "X-Request-Id", Value: "b2"}}
	b.SizeBytes = 41_000
	b.Timings = &Timings{WaitMs: 900}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("volatile headers, timings or a 2.5% size change changed the fingerprint")
	}
	b.Headers = []Header{{Name: "Server", Value: "cloudflare"}}
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("a new Server header kept the fingerprint")
	}
	c := a
	c.SizeBytes = 4_000
	if a.Fingerprint() == c.Fingerprint() {
		t.Fatal("a 90% size change kept the fingerprint")
	}
}

func TestCompare(t *testing.T) {
	exp := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	good := &ResponseFacts{StatusCode: 200, ContentType: "application/json", SizeBytes: 40_000, ServerAddr: "203.0.113.7", FinalURL: "https://api.example.com/health",
		CertExpiresAt: &exp, CertIssuer: "R11", Headers: []Header{{Name: "Age", Value: "1"}, {Name: "Server", Value: "nginx"}}}
	failing := ResponseFacts{StatusCode: 502, ContentType: "text/html", SizeBytes: 1_200, ServerAddr: "198.51.100.9", FinalURL: "https://api.example.com/health",
		CertExpiresAt: &exp, CertIssuer: "R11", Headers: []Header{{Name: "Age", Value: "99"}, {Name: "Server", Value: "cloudflare"}},
		Timings: &Timings{DNSMs: 10, ConnectMs: 20, TLSMs: 30, WaitMs: 2400}}
	usual := &Timings{DNSMs: 8, ConnectMs: 15, TLSMs: 25, WaitMs: 180}
	var lines []string
	for _, c := range Compare(good, failing, usual) {
		lines = append(lines, c.String())
	}
	want := []string{"Status: 200 → 502", "Server address: 203.0.113.7 → 198.51.100.9", "Content type: application/json → text/html",
		"Server: nginx → cloudflare", "Size: 40 KB → 1.2 KB", "Server wait: 2.4 s (usually 180 ms)"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if Compare(nil, failing, usual) != nil {
		t.Fatal("a new monitor has no last good response, so no changes")
	}
	// Same response but slow: only the timing. A small size change and a modest timing change don't count.
	same := *good
	same.SizeBytes, same.Timings = 36_000, &Timings{WaitMs: 250}
	if c := Compare(good, same, usual); len(c) != 0 {
		t.Fatalf("%+v", c)
	}
	// No response: nothing response-related is compared, the server address still is.
	gone := ResponseFacts{ServerAddr: "198.51.100.9", SizeBytes: -1}
	if c := Compare(good, gone, nil); len(c) != 1 || c[0].Field != "server_addr" {
		t.Fatalf("%+v", c)
	}
}

func TestCompareKeepsAtMostSixChanges(t *testing.T) {
	good := &ResponseFacts{StatusCode: 200, SizeBytes: 100}
	failing := ResponseFacts{StatusCode: 500, SizeBytes: 100}
	for _, n := range []string{"Cache-Control", "Content-Encoding", "Location", "Server", "Www-Authenticate", "X-Powered-By"} {
		failing.Headers = append(failing.Headers, Header{Name: n, Value: "v"})
	}
	if c := Compare(good, failing, nil); len(c) != maxChanges || c[0].Field != "status" {
		t.Fatalf("%d changes: %+v", len(c), c)
	}
}

// A load balancer answering from another address of the same network is not a change; a move to another network is. The content type is
// compared once, as the media type, never again as its header.
func TestCompareIgnoresBalancedAddressesAndCountsTheContentTypeOnce(t *testing.T) {
	good := &ResponseFacts{StatusCode: 200, SizeBytes: 100, ContentType: "application/json", ServerAddr: "2600:1f18:2da1:d401::1",
		Headers: []Header{{Name: "Content-Type", Value: "application/json"}}}
	failing := ResponseFacts{StatusCode: 200, SizeBytes: 100, ContentType: "text/html", ServerAddr: "2600:1f18:2da1:d402::9",
		Headers: []Header{{Name: "Content-Type", Value: "text/html; charset=utf-8"}}}
	c := Compare(good, failing, nil)
	if len(c) != 1 || c[0].Field != "content_type" {
		t.Fatalf("%+v", c)
	}
	failing.ServerAddr = "2a05:d014::1"
	if c := Compare(good, failing, nil); len(c) != 2 || c[0].Field != "server_addr" {
		t.Fatalf("%+v", c)
	}
	v4 := &ResponseFacts{StatusCode: 200, SizeBytes: 100, ServerAddr: "203.0.113.7"}
	if c := Compare(v4, ResponseFacts{StatusCode: 200, SizeBytes: 100, ServerAddr: "203.0.113.200"}, nil); len(c) != 0 {
		t.Fatalf("same /24: %+v", c)
	}
	if a, b := (ResponseFacts{ServerAddr: "203.0.113.7"}), (ResponseFacts{ServerAddr: "203.0.113.9"}); a.Fingerprint() != b.Fingerprint() {
		t.Fatal("a balanced address rewrote the last good response")
	}
}

// Cache state and edge names change between requests: never a change, never a new last good response.
func TestCacheHeadersAreVolatile(t *testing.T) {
	a := ResponseFacts{StatusCode: 200, SizeBytes: 100, Headers: []Header{{Name: "Cf-Cache-Status", Value: "HIT"}, {Name: "Via", Value: "1.1 abc.cloudfront.net"},
		{Name: "X-Cache", Value: "Hit from cloudfront"}}}
	b := ResponseFacts{StatusCode: 200, SizeBytes: 100, Headers: []Header{{Name: "Cf-Cache-Status", Value: "MISS"}, {Name: "Via", Value: "1.1 def.cloudfront.net"},
		{Name: "X-Cache", Value: "Miss from cloudfront"}}}
	if a.Fingerprint() != b.Fingerprint() || len(Compare(&a, b, nil)) != 0 {
		t.Fatalf("%+v", Compare(&a, b, nil))
	}
}

// A site still serving its expired certificate shows the same certificate, issuer included: no "certificate changed".
func TestAnExpiredCertificateKeepsItsIssuer(t *testing.T) {
	url, cfg := tlsServer(t, time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour), sleepy(0))
	r := check(t, cfg, Target{URL: url})
	if r.Success || r.Facts == nil || r.Facts.CertExpiresAt == nil || r.Facts.CertIssuer != "test ca" {
		t.Fatalf("%s %+v", r.ErrorCategory, r.Facts)
	}
	good := *r.Facts
	if c := Compare(&good, *r.Facts, nil); len(c) != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestExcerptHidesBasicAuthAndTheRequestBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		b, _ := io.ReadAll(r.Body)
		w.Write([]byte("Authorization: " + r.Header.Get("Authorization") + "\nbody: " + string(b)))
	}))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL, Method: "POST", CaptureBody: true, Auth: Auth{Type: AuthBasic, Username: "user", Secret: "hunter2"},
		Body: `{"client_secret":"cs-0123456789","grant":"x"}`})
	e := r.Excerpt
	if e == nil || strings.Contains(e.Text, "dXNlcjpodW50ZXIy") || strings.Contains(e.Text, "cs-0123456789") {
		t.Fatalf("not hidden: %+v", e)
	}
}

// A secret that crosses the 4 KB cut is hidden whole, not left half in the clear; the start of one that runs past what was read is dropped.
func TestASecretAtTheCutIsHidden(t *testing.T) {
	secret := "sk_live_" + strings.Repeat("A", 32)
	red := &redactor{}
	red.add(secret)
	body := []byte(strings.Repeat("y", MaxExcerptBytes-16) + secret + strings.Repeat("z", 100))
	e := excerptOf(body, false, red)
	if strings.Contains(e.Text, "sk_live_") || !e.Truncated || len(e.Text) > MaxExcerptBytes {
		t.Fatalf("%q", e.Text[len(e.Text)-40:])
	}
	if got := red.trimPartial(strings.Repeat("y", 10) + secret[:12]); got != strings.Repeat("y", 10) {
		t.Fatalf("%q", got)
	}
}

// After a redirect with no response, the address may be an earlier hop's: not compared with the last good one.
func TestNoAddressChangeAfterAFailedRedirect(t *testing.T) {
	good := &ResponseFacts{StatusCode: 200, SizeBytes: 100, ServerAddr: "198.51.100.7"}
	failing := ResponseFacts{SizeBytes: -1, ServerAddr: "203.0.113.9", Timings: &Timings{Redirects: 1}}
	if c := Compare(good, failing, nil); len(c) != 0 {
		t.Fatalf("%+v", c)
	}
}

// A destination the address rules refuse was never contacted: no facts, so no snapshot.
func TestABlockedDestinationHasNoFacts(t *testing.T) {
	if r := check(t, loopbackCfg(), Target{URL: "http://10.0.0.1/"}); r.ErrorCategory != CatBlocked || r.Facts != nil {
		t.Fatalf("%s %+v", r.ErrorCategory, r.Facts)
	}
}
