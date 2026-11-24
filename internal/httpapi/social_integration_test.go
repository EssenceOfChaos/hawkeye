package httpapi_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"hawkeye/internal/socialauth"
	"hawkeye/internal/twofactor"
)

// fakeSocial is Google's token endpoint, and GitHub's and X's token endpoints and APIs, answering for the codes a test hands out.
type fakeSocial struct {
	srv   *httptest.Server
	mu    sync.Mutex
	codes map[string]fakeAccount // code -> who signs in (and, for Google, the nonce to put in the ID token)
	n     int
}

type fakeAccount struct {
	sub      string
	email    string
	verified bool
	nonce    string
}

func newFakeSocial(t *testing.T) *fakeSocial {
	f := &fakeSocial{codes: map[string]fakeAccount{}}
	mux := http.NewServeMux()
	take := func(code string) (fakeAccount, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		a, ok := f.codes[code]
		delete(f.codes, code) // codes are single use
		return a, ok
	}
	mux.HandleFunc("POST /google/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		a, ok := take(r.PostForm.Get("code"))
		if !ok || r.PostForm.Get("code_verifier") == "" || r.PostForm.Get("client_secret") != "g-secret" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		claims, _ := json.Marshal(map[string]any{"iss": "https://accounts.google.com", "aud": "g-client", "exp": time.Now().Add(time.Hour).Unix(),
			"sub": a.sub, "email": a.email, "email_verified": a.verified, "nonce": a.nonce})
		tok := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": tok, "access_token": "unused"})
	})
	mux.HandleFunc("POST /github/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		a, ok := take(r.PostForm.Get("code"))
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "gh-" + a.sub + "|" + a.email + "|" + fmt.Sprint(a.verified)})
	})
	gh := func(r *http.Request) []string {
		return strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "gh-"), "|", 3)
	}
	mux.HandleFunc("GET /github/api/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id": %s, "login": "someone"}`, gh(r)[0])
	})
	mux.HandleFunc("GET /github/api/user/emails", func(w http.ResponseWriter, r *http.Request) {
		p := gh(r)
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"email": "old@example.net", "primary": false, "verified": true},
			{"email": p[1], "primary": true, "verified": p[2] == "true"},
		})
	})
	mux.HandleFunc("POST /x/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, secret, _ := r.BasicAuth() // X wants the client's credentials in the header, never the form
		if id != "x-client" || secret != "x-secret" || r.PostForm.Get("client_secret") != "" || r.PostForm.Get("grant_type") != "authorization_code" {
			http.Error(w, `{"error":"unauthorized_client"}`, http.StatusUnauthorized)
			return
		}
		a, ok := take(r.PostForm.Get("code"))
		if !ok || r.PostForm.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		email := ""
		if a.verified { // X sends confirmed_email only for a confirmed address
			email = a.email
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "x-" + a.sub + "|" + email, "token_type": "bearer"})
	})
	mux.HandleFunc("GET /x/api/2/users/me", func(w http.ResponseWriter, r *http.Request) {
		p := strings.SplitN(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer x-"), "|", 2)
		data := map[string]any{"id": p[0], "name": "Someone", "username": "someone"}
		if p[1] != "" && r.URL.Query().Get("user.fields") == "confirmed_email" {
			data["confirmed_email"] = p[1]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSocial) providers() map[string]socialauth.Provider {
	return map[string]socialauth.Provider{
		socialauth.Google: socialauth.NewGoogle(socialauth.Config{ClientID: "g-client", ClientSecret: "g-secret", RedirectURL: "http://app.test/cb",
			AuthEndpoint: "https://accounts.example/auth", TokenEndpoint: f.srv.URL + "/google/token"}),
		socialauth.GitHub: socialauth.NewGitHub(socialauth.Config{ClientID: "gh-client", ClientSecret: "gh-secret", RedirectURL: "http://app.test/cb",
			AuthEndpoint: "https://github.example/authorize", TokenEndpoint: f.srv.URL + "/github/token", APIBase: f.srv.URL + "/github/api"}),
		socialauth.X: socialauth.NewX(socialauth.Config{ClientID: "x-client", ClientSecret: "x-secret", RedirectURL: "http://app.test/cb",
			AuthEndpoint: "https://x.example/i/oauth2/authorize", TokenEndpoint: f.srv.URL + "/x/token", APIBase: f.srv.URL + "/x/api"}),
	}
}

func (f *fakeSocial) code(a fakeAccount) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	c := fmt.Sprintf("code-%d", f.n)
	f.codes[c] = a
	return c
}

// social runs a social sign-in the way a browser would: start, "go to" the provider (the fake approves at once), and come back to the callback.
// It returns where the callback redirected. Redirects are not followed, so the test can read them.
func (c *client) social(t *testing.T, f *fakeSocial, provider, intent, next string, who fakeAccount) *url.URL {
	t.Helper()
	r := c.call("POST", "/api/v1/auth/social/"+provider+"/start", map[string]any{"intent": intent, "next": next})
	if r.Status != 200 {
		t.Fatalf("start: %d %v", r.Status, r.Body)
	}
	auth, _ := url.Parse(r.Body["url"].(string))
	q := auth.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Fatalf("auth url %s", auth)
	}
	who.nonce = q.Get("nonce")
	return c.callback(t, provider, url.Values{"state": {q.Get("state")}, "code": {f.code(who)}})
}

func (c *client) callback(t *testing.T, provider string, q url.Values) *url.URL {
	t.Helper()
	hc := *c.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := hc.Get(c.base + "/api/v1/auth/social/" + provider + "/callback?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback: %d", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	return loc
}

func (e *env) mailedTo(t *testing.T, to, subject string) bool {
	t.Helper()
	for _, m := range e.mail.Messages() {
		if m.To == to && strings.Contains(m.Subject, subject) {
			return true
		}
	}
	return false
}

func fragment(u *url.URL) url.Values {
	v, _ := url.ParseQuery(u.Fragment)
	return v
}

func (c *client) me(t *testing.T) string {
	t.Helper()
	r := c.call("GET", "/api/v1/auth/me", nil)
	if r.Status != 200 {
		return ""
	}
	return r.Body["user"].(map[string]any)["email"].(string)
}

func TestSocialSignUpAndSignIn(t *testing.T) {
	e := newEnv(t, 100000)
	if r := e.newClient(t).call("GET", "/api/v1/auth/social/providers", nil); fmt.Sprint(r.Body["providers"]) != "[github google x]" {
		t.Fatalf("providers %v", r.Body)
	}

	// A new Google account creates a HawkEye account: verified, no password, signed in, and told where to go.
	c := e.newClient(t)
	loc := c.social(t, e.social, "google", "signin", "/monitors/new?type=http", fakeAccount{sub: "g-1", email: "New@Example.com", verified: true})
	if loc.Path != "/monitors/new" || loc.RawQuery != "type=http" || fragment(loc).Get("signed-in") != "google" || fragment(loc).Get("signed-up") == "" {
		t.Fatalf("redirect %s", loc)
	}
	if c.me(t) != "new@example.com" {
		t.Fatal("not signed in")
	}
	if n := e.countRows(t, `SELECT count(*) FROM users WHERE email = 'new@example.com' AND password_hash IS NULL AND email_verified_at IS NOT NULL`); n != 1 {
		t.Fatalf("users %d", n)
	}
	// Its own alert address is active at once, as the address is verified.
	if n := e.countRows(t, `SELECT count(*) FROM notification_settings WHERE destination = 'new@example.com' AND verified_at IS NOT NULL`); n != 1 {
		t.Fatalf("alert address %d", n)
	}
	// The same Google account signs in again, even after its email changes at Google: it is matched by ID.
	c2 := e.newClient(t)
	loc = c2.social(t, e.social, "google", "signin", "", fakeAccount{sub: "g-1", email: "renamed@example.com", verified: true})
	if loc.Path != "/dashboard" || fragment(loc).Get("signed-up") != "" || c2.me(t) != "new@example.com" {
		t.Fatalf("second sign-in %s", loc)
	}
	// A password can't be used: the account has none, and the answer is the usual one.
	if r := e.newClient(t).call("POST", "/api/v1/auth/login", map[string]string{"email": "new@example.com", "password": pw}); errCode(r) != "INVALID_CREDENTIALS" {
		t.Fatalf("login without a password: %d %v", r.Status, r.Body)
	}
	// An unsafe next is dropped.
	loc = e.newClient(t).social(t, e.social, "google", "signin", "//evil.example/x", fakeAccount{sub: "g-1", email: "new@example.com", verified: true})
	if loc.Host != "" || loc.Path != "/dashboard" {
		t.Fatalf("open redirect: %s", loc)
	}
}

func TestSocialSignInLinksAVerifiedAccountAndKeepsThePassword(t *testing.T) {
	e := newEnv(t, 100000)
	e.register(t, "dev@example.com")
	e.markVerified(t, "dev@example.com")

	// GitHub's primary, verified address matches: the identity is connected, the person is told, and the password still works.
	c := e.newClient(t)
	loc := c.social(t, e.social, "github", "signin", "", fakeAccount{sub: "4242", email: "dev@example.com", verified: true})
	if fragment(loc).Get("signed-in") != "github" || c.me(t) != "dev@example.com" {
		t.Fatalf("link: %s", loc)
	}
	if !e.mailedTo(t, "dev@example.com", "GitHub was connected") {
		t.Fatal("no connected email")
	}
	if r := e.newClient(t).call("POST", "/api/v1/auth/login", map[string]string{"email": "dev@example.com", "password": pw}); r.Status != 200 {
		t.Fatalf("password after linking: %d %v", r.Status, r.Body)
	}
	m := c.call("GET", "/api/v1/account/sign-in-methods", nil)
	ids := m.Body["identities"].([]any)
	if m.Body["has_password"] != true || len(ids) != 1 || ids[0].(map[string]any)["provider"] != "github" {
		t.Fatalf("methods %v", m.Body)
	}

	// An unverified provider address never links or creates anything.
	loc = e.newClient(t).social(t, e.social, "google", "signin", "", fakeAccount{sub: "g-x", email: "dev@example.com", verified: false})
	if loc.Path != "/login" || loc.Query().Get("error") != "email_not_verified" {
		t.Fatalf("unverified: %s", loc)
	}
	if n := e.countRows(t, `SELECT count(*) FROM user_identities WHERE provider = 'google'`); n != 0 {
		t.Fatalf("identities %d", n)
	}
}

func TestSocialSignInResetsAnUnverifiedAccountSomeoneElseRegistered(t *testing.T) {
	e := newEnv(t, 100000)
	// Someone registers the victim's address with a password they know, and never verifies it.
	squatter, _ := e.register(t, "victim@example.com")

	victim := e.newClient(t)
	loc := victim.social(t, e.social, "google", "signin", "", fakeAccount{sub: "g-v", email: "victim@example.com", verified: true})
	if fragment(loc).Get("signed-in") != "google" || victim.me(t) != "victim@example.com" {
		t.Fatalf("victim: %s", loc)
	}
	// The squatter's password and session no longer work.
	if r := e.newClient(t).call("POST", "/api/v1/auth/login", map[string]string{"email": "victim@example.com", "password": pw}); r.Status == 200 {
		t.Fatal("the squatter's password still works")
	}
	if squatter.me(t) != "" {
		t.Fatal("the squatter is still signed in")
	}
	if n := e.countRows(t, `SELECT count(*) FROM audit_events WHERE action = 'account.sign_in_method_connected' AND details->>'unverified_account_reset' = 'true'`); n != 1 {
		t.Fatalf("log entries %d", n)
	}
}

func TestSocialSignInStillAsksForTheTwoFactorCode(t *testing.T) {
	e, clk := newTwoFactorEnv(t)
	c, _ := e.register(t, "secure@example.com")
	e.markVerified(t, "secure@example.com")
	c.social(t, e.social, "google", "connect", "", fakeAccount{sub: "g-s", email: "secure@example.com", verified: true})
	secret, _ := enableTwoFactor(t, c, clk)

	b := e.newClient(t)
	loc := b.social(t, e.social, "google", "signin", "/monitors", fakeAccount{sub: "g-s", email: "secure@example.com", verified: true})
	f := fragment(loc)
	if loc.Path != "/login" || f.Get("challenge") == "" || f.Get("via") != "google" || f.Get("next") != "/monitors" {
		t.Fatalf("redirect %s", loc)
	}
	if b.me(t) != "" {
		t.Fatal("signed in before the code")
	}
	if n := e.countRows(t, `SELECT count(*) FROM login_challenges WHERE first_factor = 'google'`); n != 1 {
		t.Fatalf("challenges %d", n)
	}
	// The code page finishes it as after a password, and the security log says Google came first.
	code, _ := twofactor.Code(secret, clk.now())
	if r := b.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": f.Get("challenge"), "code": code}); r.Status != 200 {
		t.Fatalf("code: %d %v", r.Status, r.Body)
	}
	if b.me(t) != "secure@example.com" {
		t.Fatal("not signed in after the code")
	}
	if n := e.countRows(t, `SELECT count(*) FROM audit_events WHERE action = 'account.signed_in' AND details->>'via' = 'google'`); n != 1 {
		t.Fatalf("log entries %d", n)
	}
}

func TestSignInWithX(t *testing.T) {
	e, clk := newTwoFactorEnv(t)

	// A new X account with a confirmed address creates a HawkEye account, matched afterwards by X's ID.
	c := e.newClient(t)
	loc := c.social(t, e.social, "x", "signin", "", fakeAccount{sub: "1500", email: "Poster@Example.com", verified: true})
	if fragment(loc).Get("signed-in") != "x" || fragment(loc).Get("signed-up") == "" || c.me(t) != "poster@example.com" {
		t.Fatalf("sign-up: %s", loc)
	}
	if n := e.countRows(t, `SELECT count(*) FROM user_identities WHERE provider = 'x' AND subject = '1500'`); n != 1 {
		t.Fatalf("identities %d", n)
	}

	// An X account without a confirmed address (one made with a phone number) is refused, and nothing is created.
	loc = e.newClient(t).social(t, e.social, "x", "signin", "", fakeAccount{sub: "1501", email: "phone@example.com", verified: false})
	if loc.Path != "/login" || loc.Query().Get("error") != "email_not_verified" {
		t.Fatalf("no address: %s", loc)
	}

	// Connecting X to an account with two-factor on: the account owner is told, and signing in with X still asks for the code.
	owner, _ := e.register(t, "secure@example.com")
	e.markVerified(t, "secure@example.com")
	secret, _ := enableTwoFactor(t, owner, clk)
	owner.social(t, e.social, "x", "connect", "", fakeAccount{sub: "1502", email: "secure@example.com", verified: true})
	if !e.mailedTo(t, "secure@example.com", "X was connected") {
		t.Fatal("no connected email")
	}
	b := e.newClient(t)
	loc = b.social(t, e.social, "x", "signin", "", fakeAccount{sub: "1502", email: "secure@example.com", verified: true})
	if f := fragment(loc); f.Get("challenge") == "" || f.Get("via") != "x" || b.me(t) != "" {
		t.Fatalf("two-factor: %s", loc)
	}
	code, _ := twofactor.Code(secret, clk.now())
	if r := b.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": fragment(loc).Get("challenge"), "code": code}); r.Status != 200 || b.me(t) != "secure@example.com" {
		t.Fatalf("code: %d %v", r.Status, r.Body)
	}
}

func TestSocialStateIsSingleUseAndBoundToTheBrowser(t *testing.T) {
	e := newEnv(t, 100000)
	c := e.newClient(t)
	r := c.call("POST", "/api/v1/auth/social/google/start", map[string]any{"intent": "signin"})
	auth, _ := url.Parse(r.Body["url"].(string))
	state, nonce := auth.Query().Get("state"), auth.Query().Get("nonce")

	// Another browser (no state cookie) can't finish it: a link sent to someone else can't sign them in to the sender's account.
	other := e.newClient(t)
	loc := other.callback(t, "google", url.Values{"state": {state}, "code": {e.social.code(fakeAccount{sub: "g-1", email: "a@example.com", verified: true, nonce: nonce})}})
	if loc.Query().Get("error") != "expired" || other.me(t) != "" {
		t.Fatalf("other browser: %s", loc)
	}
	// Canceling at the provider uses up the state.
	loc = c.callback(t, "google", url.Values{"state": {state}, "error": {"access_denied"}})
	if loc.Query().Get("error") != "canceled" {
		t.Fatalf("canceled: %s", loc)
	}
	loc = c.callback(t, "google", url.Values{"state": {state}, "code": {e.social.code(fakeAccount{sub: "g-1", email: "a@example.com", verified: true, nonce: nonce})}})
	if loc.Query().Get("error") != "expired" || c.me(t) != "" {
		t.Fatalf("reused state: %s", loc)
	}
	// A wrong nonce (a replayed ID token) fails.
	r = c.call("POST", "/api/v1/auth/social/google/start", map[string]any{"intent": "signin"})
	auth, _ = url.Parse(r.Body["url"].(string))
	loc = c.callback(t, "google", url.Values{"state": {auth.Query().Get("state")}, "code": {e.social.code(fakeAccount{sub: "g-1", email: "a@example.com", verified: true, nonce: "replayed"})}})
	if loc.Query().Get("error") != "failed" {
		t.Fatalf("wrong nonce: %s", loc)
	}
	// Connecting needs a session.
	if r := e.newClient(t).call("POST", "/api/v1/auth/social/google/start", map[string]any{"intent": "connect"}); r.Status != 401 {
		t.Fatalf("connect signed out: %d", r.Status)
	}
	if r := e.newClient(t).call("POST", "/api/v1/auth/social/twitter/start", map[string]any{}); r.Status != 404 {
		t.Fatalf("unknown provider: %d", r.Status)
	}
}

func TestSignInMethodsWithoutAPassword(t *testing.T) {
	e := newEnv(t, 100000)
	c := e.newClient(t)
	c.social(t, e.social, "github", "signin", "", fakeAccount{sub: "7", email: "nopw@example.com", verified: true})

	// The only way in can't be removed.
	if r := c.call("DELETE", "/api/v1/account/sign-in-methods/github", nil); errCode(r) != "LAST_SIGN_IN_METHOD" {
		t.Fatalf("last method: %d %v", r.Status, r.Body)
	}
	// Sensitive actions need a fresh confirmation through the provider.
	if r := c.call("POST", "/api/v1/account/password", map[string]string{"password": "another-long-password"}); errCode(r) != "CONFIRMATION_REQUIRED" {
		t.Fatalf("set password unconfirmed: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/account/delete", map[string]string{"confirm_email": "nopw@example.com"}); errCode(r) != "CONFIRMATION_REQUIRED" {
		t.Fatalf("delete unconfirmed: %d %v", r.Status, r.Body)
	}
	// Confirming with a different GitHub account doesn't count.
	loc := c.social(t, e.social, "github", "confirm", "/account", fakeAccount{sub: "8", email: "else@example.com", verified: true})
	if loc.Query().Get("social_error") != "wrong_account" {
		t.Fatalf("wrong account: %s", loc)
	}
	loc = c.social(t, e.social, "github", "confirm", "/account?x=1#danger", fakeAccount{sub: "7", email: "nopw@example.com", verified: true})
	if loc.Path != "/account" || loc.RawQuery != "x=1" || fragment(loc).Get("confirmed") != "github" {
		t.Fatalf("confirm: %s", loc)
	}
	if r := c.call("POST", "/api/v1/account/password", map[string]string{"password": "another-long-password"}); r.Status != 204 {
		t.Fatalf("set password: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/account/password", map[string]string{"password": "another-long-password"}); errCode(r) != "PASSWORD_SET" {
		t.Fatalf("set twice: %d %v", r.Status, r.Body)
	}
	if r := e.newClient(t).call("POST", "/api/v1/auth/login", map[string]string{"email": "nopw@example.com", "password": "another-long-password"}); r.Status != 200 {
		t.Fatalf("new password: %d %v", r.Status, r.Body)
	}
	// Now GitHub can be disconnected, and the person is told.
	if r := c.call("DELETE", "/api/v1/account/sign-in-methods/github", nil); r.Status != 204 {
		t.Fatalf("disconnect: %d %v", r.Status, r.Body)
	}
	if !e.mailedTo(t, "nopw@example.com", "GitHub was disconnected") {
		t.Fatal("no disconnected email")
	}
}

func TestConnectingAProviderAlreadyUsedElsewhere(t *testing.T) {
	e := newEnv(t, 100000)
	a, _ := e.register(t, "a@example.com")
	b, _ := e.register(t, "b@example.com")
	a.social(t, e.social, "google", "connect", "", fakeAccount{sub: "g-shared", email: "shared@example.com", verified: true})
	loc := b.social(t, e.social, "google", "connect", "", fakeAccount{sub: "g-shared", email: "shared@example.com", verified: true})
	if loc.Query().Get("social_error") != "identity_in_use" {
		t.Fatalf("in use: %s", loc)
	}
	loc = a.social(t, e.social, "google", "connect", "", fakeAccount{sub: "g-second", email: "a2@example.com", verified: true})
	if loc.Query().Get("social_error") != "provider_connected" {
		t.Fatalf("second google: %s", loc)
	}
}
