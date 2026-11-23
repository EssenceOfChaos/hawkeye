package checks

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRequestHeadersBodyAndMethod(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.Store([]string{r.Method, r.Header.Get("X-Env"), r.Header.Get("Content-Type"), string(b), r.Header.Get("User-Agent")})
	}))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL, Method: "POST", Body: `{"ping":true}`,
		Headers: []Header{{Name: "X-Env", Value: "prod"}, {Name: "User-Agent", Value: "spoof"}}})
	if !r.Success {
		t.Fatalf("%+v", r)
	}
	g := got.Load().([]string)
	if g[0] != "POST" || g[1] != "prod" || g[2] != "application/json" || g[3] != `{"ping":true}` || !strings.HasPrefix(g[4], "Hawkeye-Monitor/") {
		t.Fatalf("server saw %q", g)
	}

	// A custom Content-Type wins over detection; non-JSON bodies default to text/plain.
	check(t, loopbackCfg(), Target{URL: srv.URL, Method: "PUT", Body: "a=1", Headers: []Header{{Name: "Content-Type", Value: "application/x-www-form-urlencoded"}}})
	if g := got.Load().([]string); g[2] != "application/x-www-form-urlencoded" {
		t.Fatalf("content type %q", g[2])
	}
	check(t, loopbackCfg(), Target{URL: srv.URL, Method: "PUT", Body: "plain"})
	if g := got.Load().([]string); !strings.HasPrefix(g[2], "text/plain") {
		t.Fatalf("content type %q", g[2])
	}
}

func TestAuthTypes(t *testing.T) {
	var auth, key atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		key.Store(r.Header.Get("X-Api-Key"))
	}))
	defer srv.Close()
	check(t, loopbackCfg(), Target{URL: srv.URL, Auth: Auth{Type: AuthBasic, Username: "u", Secret: "p"}})
	if auth.Load() != "Basic "+base64.StdEncoding.EncodeToString([]byte("u:p")) {
		t.Fatalf("basic: %v", auth.Load())
	}
	check(t, loopbackCfg(), Target{URL: srv.URL, Auth: Auth{Type: AuthBearer, Secret: "tok"}})
	if auth.Load() != "Bearer tok" {
		t.Fatalf("bearer: %v", auth.Load())
	}
	check(t, loopbackCfg(), Target{URL: srv.URL, Auth: Auth{Type: AuthHeader, HeaderName: "X-API-Key", Secret: "k"}})
	if key.Load() != "k" || auth.Load() != "" {
		t.Fatalf("header: %v %v", key.Load(), auth.Load())
	}
}

func TestCredentialsAreNotForwardedToAnotherHost(t *testing.T) {
	var leaked atomic.Value
	leaked.Store("")
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("X-Api-Key") + r.Header.Get("Authorization"))
	}))
	defer other.Close()
	var sameHost atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		sameHost.Store(r.Header.Get("X-Api-Key") + r.Header.Get("Authorization"))
		// Same IP, different port: a different host, so credentials must not follow.
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()
	for _, a := range []Auth{{Type: AuthHeader, HeaderName: "X-Api-Key", Secret: "k"}, {Type: AuthBearer, Secret: "t"}} {
		r := check(t, loopbackCfg(), Target{URL: srv.URL, Auth: a})
		if !r.Success {
			t.Fatalf("%+v", r)
		}
		if leaked.Load() != "" {
			t.Fatalf("%s credentials sent to another host: %q", a.Type, leaked.Load())
		}
		if sameHost.Load() == "" {
			t.Fatalf("%s credentials should follow a same-host redirect", a.Type)
		}
	}
}

func TestAssertions(t *testing.T) {
	body := `{"status":"ok","version":2,"queue":512,"load":0.6,"count":"42","healthy":true,"checks":[{"name":"db","up":true}],"tags":["a","b"],"none":null}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/html" {
			w.Write([]byte("<html>Welcome back</html>"))
			return
		}
		if r.URL.Path == "/err" {
			w.WriteHeader(500)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	jp := func(path, op, v string) Assertion {
		return Assertion{Type: AssertJSONPath, Path: path, Operator: op, Value: v}
	}
	cases := []struct {
		name    string
		path    string
		as      []Assertion
		ok      bool
		summary string
	}{
		{"contains", "/html", []Assertion{{Type: AssertBodyContains, Value: "Welcome"}}, true, ""},
		{"contains fails", "/html", []Assertion{{Type: AssertBodyContains, Value: "Goodbye"}}, false, `does not contain "Goodbye"`},
		{"not contains", "/html", []Assertion{{Type: AssertBodyNotContains, Value: "error"}}, true, ""},
		{"not contains fails", "/html", []Assertion{{Type: AssertBodyNotContains, Value: "Welcome"}}, false, `contains "Welcome"`},
		{"string equals", "/", []Assertion{jp("status", OpEquals, "ok")}, true, ""},
		{"dollar prefix", "/", []Assertion{jp("$.status", OpEquals, "ok")}, true, ""},
		{"number equals", "/", []Assertion{jp("version", OpEquals, "2.0")}, true, ""},
		{"bool equals", "/", []Assertion{jp("healthy", OpEquals, "true")}, true, ""},
		{"null equals", "/", []Assertion{jp("none", OpEquals, "null")}, true, ""},
		{"array index", "/", []Assertion{jp("checks[0].up", OpEquals, "true")}, true, ""},
		{"array contains", "/", []Assertion{jp("tags", OpContains, "b")}, true, ""},
		{"string contains", "/", []Assertion{jp("status", OpContains, "o")}, true, ""},
		{"exists", "/", []Assertion{jp("checks[0].name", OpExists, "")}, true, ""},
		{"not exists", "/", []Assertion{jp("error", OpNotExists, "")}, true, ""},
		{"not equals", "/", []Assertion{jp("status", OpNotEquals, "down")}, true, ""},
		{"equals fails", "/", []Assertion{jp("status", OpEquals, "down")}, false, `JSON path status does not equal "down"`},
		{"missing path", "/", []Assertion{jp("checks[3].up", OpEquals, "true")}, false, "JSON path checks[3].up not found"},
		{"not exists fails", "/", []Assertion{jp("status", OpNotExists, "")}, false, "JSON path status exists"},
		{"not json", "/html", []Assertion{jp("status", OpExists, "")}, false, "not valid JSON"},
		{"first failure wins", "/", []Assertion{jp("status", OpEquals, "ok"), {Type: AssertBodyContains, Value: "zzz"}, jp("x", OpExists, "")}, false, `does not contain "zzz"`},
		{"status checked first", "/err", []Assertion{jp("status", OpEquals, "ok")}, false, "Received HTTP 500"},
		{"greater than", "/", []Assertion{jp("queue", OpGreaterThan, "500")}, true, ""},
		{"greater than fails", "/", []Assertion{jp("queue", OpGreaterThan, "512")}, false, "JSON path queue is not greater than 512"},
		{"at least", "/", []Assertion{jp("queue", OpGreaterOrEqual, "512")}, true, ""},
		{"less than", "/", []Assertion{jp("load", OpLessThan, "0.75")}, true, ""},
		{"less than fails", "/", []Assertion{jp("load", OpLessThan, "0.5")}, false, "JSON path load is not less than 0.5"},
		{"at most", "/", []Assertion{jp("queue", OpLessOrEqual, "512")}, true, ""},
		{"at most fails", "/", []Assertion{jp("queue", OpLessOrEqual, "511.9")}, false, "is not at most 511.9"},
		{"negative and exponent", "/", []Assertion{jp("version", OpGreaterThan, "-1e3")}, true, ""},
		{"numeric string", "/", []Assertion{jp("count", OpGreaterThan, "40")}, true, ""},
		{"not a number", "/", []Assertion{jp("status", OpLessThan, "5")}, false, "JSON path status is not a number"},
		{"bool is not a number", "/", []Assertion{jp("healthy", OpGreaterThan, "0")}, false, "is not a number"},
		{"number missing", "/", []Assertion{jp("nope", OpGreaterThan, "0")}, false, "JSON path nope not found"},
	}
	for _, c := range cases {
		r := check(t, loopbackCfg(), Target{URL: srv.URL + c.path, Assertions: c.as})
		if r.Success != c.ok || !strings.Contains(r.ErrorSummary, c.summary) {
			t.Errorf("%s: %+v", c.name, r)
		}
		if !c.ok && c.path != "/err" && r.ErrorCategory != CatAssertion {
			t.Errorf("%s: category %q", c.name, r.ErrorCategory)
		}
		if !c.ok && (r.StatusCode == nil || r.ResponseTimeMs == nil) {
			t.Errorf("%s: an assertion failure still records the response: %+v", c.name, r)
		}
	}
}

func TestLargeBodies(t *testing.T) {
	big := strings.Repeat("x", MaxBodyBytes) + "tail"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(big)) }))
	defer srv.Close()
	// Text assertions search only the first MaxBodyBytes.
	if r := check(t, loopbackCfg(), Target{URL: srv.URL, Assertions: []Assertion{{Type: AssertBodyContains, Value: "tail"}}}); r.Success {
		t.Fatalf("text beyond the limit must not be found: %+v", r)
	}
	r := check(t, loopbackCfg(), Target{URL: srv.URL, Assertions: []Assertion{{Type: AssertJSONPath, Path: "a", Operator: OpExists}}})
	if r.Success || !strings.Contains(r.ErrorSummary, "too large") {
		t.Fatalf("%+v", r)
	}
}

func TestLongSummariesAreTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{}")) }))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL, Assertions: []Assertion{{Type: AssertBodyContains, Value: strings.Repeat("é", 500)}}})
	if n := len([]rune(r.ErrorSummary)); r.Success || n != maxSummaryLen {
		t.Fatalf("summary has %d characters", n)
	}
}

func TestValidatePath(t *testing.T) {
	for _, p := range []string{"a", "$.a", "a.b", "a[0]", "a[0][1].b", "[0].a", "$[2]"} {
		if err := ValidatePath(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range []string{"", "$", "a..b", "a[", "a[x]", "a[-1]", "a[0]b", "a]"} {
		if ValidatePath(p) == nil {
			t.Errorf("%q should be invalid", p)
		}
	}
}

func TestNumberSummariesNeverShowTheValueFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"secret_count":987654}`)) }))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL, Assertions: []Assertion{{Type: AssertJSONPath, Path: "secret_count", Operator: OpLessThan, Value: "10"}}})
	if r.Success || strings.Contains(r.ErrorSummary, "987654") {
		t.Fatalf("%+v", r)
	}
}

func TestParseNumber(t *testing.T) {
	for _, s := range []string{"0", "500", " 12 ", "-3", "0.25", "1e6"} {
		if _, ok := ParseNumber(s); !ok {
			t.Errorf("%q should be a number", s)
		}
	}
	for _, s := range []string{"", "abc", "NaN", "Inf", "-Infinity", "12px"} {
		if _, ok := ParseNumber(s); ok {
			t.Errorf("%q should not be a number", s)
		}
	}
}

func TestTryRequestShowsTheResponseAndEveryCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/binary":
			w.Write([]byte{0xff, 0xfe, 0x00, 0x01})
		case "/big":
			w.Write([]byte(strings.Repeat("é", MaxPreviewBytes))) // twice the preview limit in bytes
		case "/down":
			w.WriteHeader(503)
			w.Write([]byte(`{"status":"down"}`))
		default:
			w.Write([]byte(`{"status":"ok","queue":7}`))
		}
	}))
	defer srv.Close()
	c := NewChecker(loopbackCfg())
	jp := func(path, op, v string) Assertion { return Assertion{Type: AssertJSONPath, Path: path, Operator: op, Value: v} }
	as := []Assertion{jp("status", OpEquals, "ok"), jp("queue", OpGreaterThan, "10"), jp("queue", OpLessThan, "10")}

	res, p, err := c.TryRequest(t.Context(), Target{URL: srv.URL, Method: "GET", Timeout: 5 * time.Second, Assertions: as})
	if err != nil || res.Success || p.Body != `{"status":"ok","queue":7}` || p.Truncated || p.Binary {
		t.Fatalf("%+v %+v %v", res, p, err)
	}
	// Every check is reported, not only the first failure.
	if len(p.Checks) != 3 || p.Checks[0] != "" || !strings.Contains(p.Checks[1], "not greater than 10") || p.Checks[2] != "" {
		t.Fatalf("checks %q", p.Checks)
	}
	// The scheduled check's outcome is the same as Check's.
	if res.ErrorCategory != CatAssertion || !strings.Contains(res.ErrorSummary, "not greater than 10") {
		t.Fatalf("%+v", res)
	}

	// A failing status still shows the body and each rule.
	res, p, _ = c.TryRequest(t.Context(), Target{URL: srv.URL + "/down", Method: "GET", Timeout: 5 * time.Second, Assertions: as[:1]})
	if res.Success || res.ErrorCategory != CatHTTPStatus || p.Body != `{"status":"down"}` || len(p.Checks) != 1 || p.Checks[0] == "" {
		t.Fatalf("%+v %+v", res, p)
	}

	_, p, _ = c.TryRequest(t.Context(), Target{URL: srv.URL + "/binary", Method: "GET", Timeout: 5 * time.Second})
	if !p.Binary || p.Body != "" {
		t.Fatalf("binary: %+v", p)
	}
	_, p, _ = c.TryRequest(t.Context(), Target{URL: srv.URL + "/big", Method: "GET", Timeout: 5 * time.Second})
	if !p.Truncated || len(p.Body) > MaxPreviewBytes || !utf8.ValidString(p.Body) {
		t.Fatalf("big: truncated=%v len=%d", p.Truncated, len(p.Body))
	}
}
