package checks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	password = `pa"ss-w0rd`       // a quote, so a JSON body must escape it
	token    = "tok-abc123xyz987" // what /login hands out
	session  = "sess-77881122"
)

// api is a small service a workflow can sign in to.
type api struct {
	srv      *httptest.Server
	requests atomic.Int32
	query    atomic.Value // the last ?q= /search received
}

func newAPI(t *testing.T) *api {
	a := &api{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ User, Password string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Password != password {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: session, Path: "/"})
		w.Header().Set("X-Account", "acct-42")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"token":"`+token+`","expires":3600,"roles":["admin"]}}`)
	})
	mux.HandleFunc("GET /account", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"plan":"pro","id":42}`)
	})
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "42" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err != nil || c.Value != session {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "hello")
	})
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		a.query.Store(r.URL.Query().Get("q"))
	})
	mux.HandleFunc("GET /fail/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error mentioning "+token, http.StatusInternalServerError)
	})
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.requests.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *api) step(name, method, path string) Step {
	return Step{Name: name, Target: Target{URL: a.srv.URL + path, Method: method, Timeout: 2 * time.Second}}
}

func (a *api) login() Step {
	s := a.step("Sign in", "POST", "/login")
	s.Target.Body = `{"user":"monitor@example.com","password":"{{secret.password}}"}`
	s.Extractions = []Extraction{{Name: "token", From: ExtractJSONPath, Path: "data.token"}}
	return s
}

func (a *api) account() Step {
	s := a.step("Get account", "GET", "/account")
	s.Target.Auth = Auth{Type: AuthBearer, Secret: "{{token}}"}
	s.Target.ExpectedStatus = intp(200)
	s.Target.Assertions = []Assertion{{Type: AssertJSONPath, Path: "plan", Operator: OpEquals, Value: "pro"}}
	s.Extractions = []Extraction{{Name: "id", From: ExtractJSONPath, Path: "id"}}
	return s
}

func runWF(t *testing.T, w Workflow) Result {
	t.Helper()
	if w.Secrets == nil {
		w.Secrets = map[string]string{"password": password}
	}
	res, err := NewChecker(loopbackCfg()).RunWorkflow(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func names(r Result) []string {
	var out []string
	for _, s := range r.Steps {
		out = append(out, s.Name)
	}
	return out
}

func TestWorkflowSignsInAndCarriesValuesForward(t *testing.T) {
	a := newAPI(t)
	r := runWF(t, Workflow{Steps: []Step{a.login(), a.account(), a.step("Get item", "GET", "/items/{{id}}")}})
	if !r.Success || len(r.Steps) != 3 || r.ErrorCategory != "" || *r.StatusCode != 200 {
		t.Fatalf("%+v", r)
	}
	sum := 0
	for _, s := range r.Steps {
		if !s.Success || s.ResponseTimeMs == nil {
			t.Fatalf("step %+v", s)
		}
		sum += *s.ResponseTimeMs
	}
	if *r.ResponseTimeMs != sum {
		t.Fatalf("the run's time is the steps' total: %d, steps %d", *r.ResponseTimeMs, sum)
	}
}

func TestWorkflowStopsAtTheFirstFailingStepAndNamesIt(t *testing.T) {
	a := newAPI(t)
	r := runWF(t, Workflow{Steps: []Step{a.step("Home", "GET", "/me"), a.account()}})
	if r.Success || len(r.Steps) != 1 || r.ErrorCategory != CatHTTPStatus || r.ErrorSummary != "Step 1 (Home): Received HTTP 401" {
		t.Fatalf("%+v", r)
	}

	acc := a.account()
	acc.Target.Auth.Secret = "wrong"
	r = runWF(t, Workflow{Steps: []Step{a.login(), acc, a.step("Never", "GET", "/items/42")}})
	if r.Success || strings.Join(names(r), ",") != "Sign in,Get account" || r.ErrorSummary != "Step 2 (Get account): Received HTTP 401, expected 200" || *r.StatusCode != 401 {
		t.Fatalf("%+v", r)
	}
	if r.Steps[0].Success != true || r.Steps[1].ErrorCategory != CatHTTPStatus {
		t.Fatalf("steps %+v", r.Steps)
	}
}

func TestABrokenWorkflowIsToldApartFromABrokenService(t *testing.T) {
	a := newAPI(t)
	before := a.requests.Load()
	r := runWF(t, Workflow{Steps: []Step{a.account()}})
	if r.Success || r.ErrorCategory != CatWorkflowConfig || !strings.Contains(r.ErrorSummary, "{{token}} has no value") {
		t.Fatalf("%+v", r)
	}
	if a.requests.Load() != before {
		t.Fatal("a step with a missing value was sent anyway")
	}

	r = runWF(t, Workflow{Steps: []Step{a.login()}, Secrets: map[string]string{}})
	if r.ErrorCategory != CatWorkflowConfig || !strings.Contains(r.ErrorSummary, "the secret password is not set") {
		t.Fatalf("%+v", r)
	}

	login := a.login()
	login.Extractions = []Extraction{{Name: "token", From: ExtractJSONPath, Path: "data.missing"}}
	r = runWF(t, Workflow{Steps: []Step{login}})
	if r.ErrorCategory != CatWorkflowConfig || r.ErrorSummary != "Step 1 (Sign in): JSON path data.missing not in the response" {
		t.Fatalf("%+v", r)
	}
}

func TestExtractsHeadersCookiesAndNonStringValues(t *testing.T) {
	a := newAPI(t)
	login := a.login()
	login.Extractions = []Extraction{
		{Name: "acct", From: ExtractHeader, Path: "x-account"},
		{Name: "sess", From: ExtractCookie, Path: "session"},
		{Name: "ttl", From: ExtractJSONPath, Path: "data.expires"},
		{Name: "roles", From: ExtractJSONPath, Path: "data.roles"},
	}
	search := a.step("Search", "GET", "/search?q={{acct}}|{{sess}}|{{ttl}}|{{roles}}")
	r := runWF(t, Workflow{Steps: []Step{login, search}})
	if !r.Success {
		t.Fatalf("%+v", r)
	}
	if got := a.query.Load(); got != `acct-42|`+session+`|3600|["admin"]` {
		t.Fatalf("query %q", got)
	}
}

func TestValuesAreEscapedForWhereTheyGo(t *testing.T) {
	a := newAPI(t)
	// The password contains a quote: the JSON body stays valid (the login would fail otherwise).
	odd := "a b/c&d=e?f#g"
	search := a.step("Search", "GET", "/search?q={{secret.q}}")
	r := runWF(t, Workflow{Steps: []Step{a.login(), search}, Secrets: map[string]string{"password": password, "q": odd}})
	if !r.Success || a.query.Load() != odd {
		t.Fatalf("%+v, query %q", r, a.query.Load())
	}

	// A value can't add a header line.
	h := a.step("Header", "GET", "/me")
	h.Target.Headers = []Header{{Name: "X-Test", Value: "{{secret.v}}"}}
	r = runWF(t, Workflow{Steps: []Step{h}, Secrets: map[string]string{"v": "a\r\nX-Injected: 1"}})
	if r.ErrorCategory != CatWorkflowConfig || !strings.Contains(r.ErrorSummary, "line break") {
		t.Fatalf("%+v", r)
	}
}

func TestAValueCantSendAStepSomewhereItMustNotGo(t *testing.T) {
	a := newAPI(t)
	// A host taken from a value is still checked after substitution: a private address is refused.
	s := Step{Name: "Internal", Target: Target{URL: "http://{{secret.host}}/", Method: "GET", Timeout: time.Second}}
	r := runWF(t, Workflow{Steps: []Step{s}, Secrets: map[string]string{"host": "10.0.0.1"}})
	if r.Success || r.ErrorCategory != CatBlocked {
		t.Fatalf("%+v", r)
	}
	// A value can't replace the scheme and host of an address that starts with one: it is escaped into a single piece.
	s = Step{Name: "Swap", Target: Target{URL: a.srv.URL + "/search?q={{secret.u}}", Method: "GET", Timeout: time.Second}}
	r = runWF(t, Workflow{Steps: []Step{s}, Secrets: map[string]string{"u": "x@10.0.0.1/"}})
	if !r.Success || a.query.Load() != "x@10.0.0.1/" {
		t.Fatalf("%+v, %q", r, a.query.Load())
	}
}

func TestSecretsAndExtractedValuesNeverReachTheResult(t *testing.T) {
	a := newAPI(t)
	fail := a.step("Use token", "GET", "/fail/{{token}}")
	r := runWF(t, Workflow{Steps: []Step{a.login(), fail}})
	if r.Success || r.ErrorCategory != CatHTTPStatus || r.ResponseTimeMs == nil {
		t.Fatalf("%+v", r)
	}
	b, _ := json.Marshal(r)
	for _, v := range []string{password, token, `pa\"ss-w0rd`} {
		if strings.Contains(string(b), v) {
			t.Fatalf("%q leaked into %s", v, b)
		}
	}
	if !strings.Contains(r.FinalURL, "/fail/[hidden]") {
		t.Fatalf("final URL %q", r.FinalURL)
	}
	// A step named after a secret (by mistake) doesn't show it either.
	named := a.step("Uses "+password, "GET", "/nope")
	r = runWF(t, Workflow{Steps: []Step{named}})
	if strings.Contains(r.ErrorSummary, password) || r.Steps[0].Name != "Uses [hidden]" {
		t.Fatalf("%+v", r)
	}
}

func TestCookiesAreKeptOnlyWhenAsked(t *testing.T) {
	a := newAPI(t)
	steps := []Step{a.login(), a.step("Me", "GET", "/me")}
	if r := runWF(t, Workflow{Steps: steps}); r.Success {
		t.Fatalf("without a jar the session cookie must not be sent: %+v", r)
	}
	if r := runWF(t, Workflow{Steps: steps, Cookies: true}); !r.Success {
		t.Fatalf("with a jar: %+v", r)
	}
}

func TestARunHasAnOverallTimeLimit(t *testing.T) {
	a := newAPI(t)
	// The first step makes the clock jump past the run's limit, as if it had taken a minute.
	var skew atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { skew.Store(int64(WorkflowTimeout + time.Second)) }))
	defer slow.Close()
	c := NewChecker(loopbackCfg())
	c.now = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
	steps := []Step{{Name: "Slow", Target: Target{URL: slow.URL, Method: "GET", Timeout: time.Second}}, a.step("Me", "GET", "/me")}
	before := a.requests.Load()
	r, err := c.RunWorkflow(t.Context(), Workflow{Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	if r.Success || r.ErrorCategory != CatTimeout || r.ErrorSummary != "Step 2 (Me): The run took longer than 60 seconds" || a.requests.Load() != before {
		t.Fatalf("%+v", r)
	}
}

func TestValidateExtraction(t *testing.T) {
	for _, x := range []Extraction{{Name: "token", From: ExtractJSONPath, Path: "data.token"}, {Name: "_loc", From: ExtractHeader, Path: "Location"}} {
		if err := ValidateExtraction(x); err != nil {
			t.Errorf("%+v: %v", x, err)
		}
	}
	for _, x := range []Extraction{{Name: "1x", From: ExtractHeader, Path: "A"}, {Name: "x", From: "body", Path: "a"}, {Name: "x", From: ExtractCookie, Path: "a b"}, {Name: "x", From: ExtractJSONPath, Path: "a..b"}} {
		if err := ValidateExtraction(x); err == nil {
			t.Errorf("%+v accepted", x)
		}
	}
	if got := strings.Join(Placeholders("{{a}}/{{ secret.b }}/{{ c"), ","); got != "a,secret.b" {
		t.Fatalf("placeholders %q", got)
	}
}
