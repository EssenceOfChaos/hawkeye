package httpapi_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hawkeye/internal/email"
	"hawkeye/internal/oauth"
	"hawkeye/internal/twofactor"
)

const claudeRedirect = "https://claude.ai/api/mcp/auth_callback"

// registerClient adds an OAuth client as cmd/oauthclient does, returning its ID and secret (empty for a public client).
func (e *env) registerClient(t *testing.T, public bool) (string, string) {
	t.Helper()
	svc := oauth.NewService(e.pool, nil, nil, &email.Memory{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "http://app.test")
	c, secret, err := svc.CreateClient(t.Context(), "Claude", []string{claudeRedirect}, public)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, secret
}

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 20) + "-verifier_0123456789abcdefghij"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type flow struct {
	clientID, secret, verifier, challenge, state string
	scope                                        string // what the client asks for
}

func (f flow) query() url.Values {
	q := url.Values{"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {claudeRedirect}, "state": {f.state},
		"code_challenge": {f.challenge}, "code_challenge_method": {"S256"}, "resource": {"http://app.test/mcp"}}
	if f.scope != "" {
		q.Set("scope", f.scope)
	}
	return q
}

func newFlow(t *testing.T, e *env, public bool, scope string) flow {
	id, secret := e.registerClient(t, public)
	v, c := pkce()
	return flow{clientID: id, secret: secret, verifier: v, challenge: c, state: "xyz", scope: scope}
}

// consent runs the browser part: the authorization endpoint, the consent page's API, and approval. It returns the approval response.
func consent(t *testing.T, e *env, c *client, f flow, body map[string]any) resp {
	t.Helper()
	res, err := noRedirects.Get(e.srv.URL + "/oauth/authorize?" + f.query().Encode())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode != 302 || !strings.HasPrefix(loc, "http://app.test/connect?") {
		t.Fatalf("authorize: %d %s", res.StatusCode, loc)
	}
	q := strings.TrimPrefix(loc, "http://app.test/connect?")
	if info := c.call("GET", "/api/v1/oauth/authorize?"+q, nil); info.Status != 200 || info.Body["client"].(map[string]any)["name"] != "Claude" ||
		info.Body["redirect_host"] != "claude.ai" || info.Body["redirect_loopback"] != false {
		t.Fatalf("info: %d %v", info.Status, info.Body)
	}
	parsed, _ := url.ParseQuery(q)
	req := map[string]any{}
	for k := range parsed {
		req[k] = parsed.Get(k)
	}
	for k, v := range body {
		req[k] = v
	}
	return c.call("POST", "/api/v1/oauth/authorize", req)
}

func codeFrom(t *testing.T, r resp) string {
	t.Helper()
	if r.Status != 200 {
		t.Fatalf("approve: %d %v", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Body["redirect"].(string))
	if !strings.HasPrefix(r.Body["redirect"].(string), claudeRedirect+"?") || u.Query().Get("state") != "xyz" || u.Query().Get("iss") != "http://app.test" {
		t.Fatalf("redirect %s", u)
	}
	return u.Query().Get("code")
}

func tokenRequest(t *testing.T, e *env, f flow, form url.Values) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if f.secret != "" {
		req.SetBasicAuth(f.clientID, f.secret)
	} else {
		form.Set("client_id", f.clientID)
		req.Body = io.NopCloser(strings.NewReader(form.Encode()))
		req.ContentLength = int64(len(form.Encode()))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func exchange(t *testing.T, e *env, f flow, code string) (int, map[string]any) {
	return tokenRequest(t, e, f, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {claudeRedirect},
		"code_verifier": {f.verifier}, "resource": {"http://app.test/mcp"}})
}

func mcpStatus(t *testing.T, e *env, token string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestOAuthDiscovery(t *testing.T) {
	e := newEnv(t, 1000)
	get := func(path string) map[string]any {
		res, err := http.Get(e.srv.URL + path)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, res.StatusCode, err)
		}
		defer res.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(res.Body).Decode(&m)
		return m
	}
	as := get("/.well-known/oauth-authorization-server")
	if as["issuer"] != "http://app.test" || as["token_endpoint"] != "http://app.test/oauth/token" || as["code_challenge_methods_supported"].([]any)[0] != "S256" {
		t.Fatalf("%v", as)
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		if pr := get(p); pr["resource"] != "http://app.test/mcp" || pr["authorization_servers"].([]any)[0] != "http://app.test" {
			t.Fatalf("%s: %v", p, pr)
		}
	}
	// /mcp tells a client without a token where to start.
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{}`))
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), `resource_metadata="http://app.test/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("%d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
}

func TestOAuthConnectUseRefreshAndDisconnect(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	f := newFlow(t, e, false, "read write")

	code := codeFrom(t, consent(t, e, owner, f, map[string]any{"organization_id": org, "access": "write"}))
	if countMail(e, "owner@example.com", "An app was connected") != 1 {
		t.Fatal("no email about the connection")
	}
	status, tok := exchange(t, e, f, code)
	if status != 200 || tok["token_type"] != "Bearer" || tok["expires_in"] != float64(3600) || tok["scope"] != "write" ||
		!strings.HasPrefix(tok["access_token"].(string), "hk_") || !strings.HasPrefix(tok["refresh_token"].(string), "hkr_") {
		t.Fatalf("%d %v", status, tok)
	}
	access := tok["access_token"].(string)

	// The access token works on the MCP server, with write access, and nowhere else.
	s := e.mcpSession(t, access)
	if r := callTool(t, s, "get_account_overview", nil); r.IsError || r.Data["can_change_data"] != true {
		t.Fatalf("%+v", r)
	}
	if r := e.keyClient(t, access).call("GET", "/api/v1/monitors", nil); r.Status != 403 || errCode(r) != "TOKEN_AUDIENCE" {
		t.Fatalf("the REST API refuses OAuth tokens: %d %v", r.Status, r.Body)
	}
	// It is not one of the person's API keys, but it is a connected app.
	if keys := owner.call("GET", "/api/v1/api-keys", nil).Body["keys"].([]any); len(keys) != 0 {
		t.Fatalf("%v", keys)
	}
	apps := owner.call("GET", "/api/v1/oauth/connected-apps", nil).Body["connected_apps"].([]any)
	if len(apps) != 1 || apps[0].(map[string]any)["client_name"] != "Claude" || apps[0].(map[string]any)["scope"] != "write" {
		t.Fatalf("%v", apps)
	}

	// Refresh tokens rotate; presenting a used one ends the whole connection.
	oldRefresh := tok["refresh_token"].(string)
	status, tok2 := tokenRequest(t, e, f, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {oldRefresh}})
	if status != 200 || tok2["refresh_token"] == oldRefresh {
		t.Fatalf("%d %v", status, tok2)
	}
	if mcpStatus(t, e, tok2["access_token"].(string)) != 200 {
		t.Fatal("the new access token works")
	}
	if status, body := tokenRequest(t, e, f, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {oldRefresh}}); status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("reusing a refresh token: %d %v", status, body)
	}
	if mcpStatus(t, e, tok2["access_token"].(string)) != 401 || mcpStatus(t, e, access) != 401 {
		t.Fatal("reuse revokes every token of the connection")
	}
	if apps := owner.call("GET", "/api/v1/oauth/connected-apps", nil).Body["connected_apps"].([]any); len(apps) != 0 {
		t.Fatalf("%v", apps)
	}

	// Connecting again and disconnecting from the API keys page.
	code = codeFrom(t, consent(t, e, owner, f, map[string]any{"organization_id": org, "access": "read"}))
	_, tok3 := exchange(t, e, f, code)
	if tok3["scope"] != "read" || mcpStatus(t, e, tok3["access_token"].(string)) != 200 {
		t.Fatalf("%v", tok3)
	}
	id := owner.call("GET", "/api/v1/oauth/connected-apps", nil).Body["connected_apps"].([]any)[0].(map[string]any)["id"].(string)
	if r := owner.call("DELETE", "/api/v1/oauth/connected-apps/"+id, nil); r.Status != 204 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if mcpStatus(t, e, tok3["access_token"].(string)) != 401 {
		t.Fatal("disconnecting ends the access token at once")
	}
	if status, body := tokenRequest(t, e, f, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok3["refresh_token"].(string)}}); status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("%d %v", status, body)
	}
}

func TestOAuthRefusesBadRequests(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	f := newFlow(t, e, false, "read write")

	// Problems with the client or redirect URI are shown, never redirected.
	for _, q := range []url.Values{
		func() url.Values { q := f.query(); q.Set("client_id", "hkc_nope"); return q }(),
		func() url.Values { q := f.query(); q.Set("redirect_uri", "https://evil.example/cb"); return q }(),
	} {
		res, _ := noRedirects.Get(e.srv.URL + "/oauth/authorize?" + q.Encode())
		res.Body.Close()
		if res.StatusCode != 400 || res.Header.Get("Location") != "" {
			t.Fatalf("%v: %d %s", q, res.StatusCode, res.Header.Get("Location"))
		}
	}
	// Other problems go back to the client's registered redirect.
	for name, q := range map[string]url.Values{
		"no PKCE":  func() url.Values { q := f.query(); q.Del("code_challenge"); return q }(),
		"plain":    func() url.Values { q := f.query(); q.Set("code_challenge_method", "plain"); return q }(),
		"resource": func() url.Values { q := f.query(); q.Set("resource", "https://other.example/mcp"); return q }(),
		"scope":    func() url.Values { q := f.query(); q.Set("scope", "admin"); return q }(),
		"implicit": func() url.Values { q := f.query(); q.Set("response_type", "token"); return q }(),
	} {
		res, _ := noRedirects.Get(e.srv.URL + "/oauth/authorize?" + q.Encode())
		res.Body.Close()
		if loc := res.Header.Get("Location"); res.StatusCode != 302 || !strings.HasPrefix(loc, claudeRedirect+"?error=") {
			t.Fatalf("%s: %d %s", name, res.StatusCode, loc)
		}
	}

	// Write access needs a plan with API writes, and the client to have asked for it.
	if r := consent(t, e, owner, f, map[string]any{"organization_id": org, "access": "write"}); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("Free: %d %v", r.Status, r.Body)
	}
	e.setOrgPlan(t, org, "pro")
	readOnly := f
	readOnly.scope = "read"
	if r := consent(t, e, owner, readOnly, map[string]any{"organization_id": org, "access": "write"}); r.Status != 400 || errCode(r) != "INVALID_SCOPE" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	_, other := e.register(t, "other@example.com")
	if r := consent(t, e, owner, f, map[string]any{"organization_id": other, "access": "read"}); r.Status != 403 {
		t.Fatalf("someone else's organization: %d", r.Status)
	}

	// The token endpoint checks the client, the verifier and the redirect, and a code works once.
	code := codeFrom(t, consent(t, e, owner, f, map[string]any{"organization_id": org, "access": "read"}))
	wrongSecret := f
	wrongSecret.secret = "hkcs_wrong"
	if status, body := exchange(t, e, wrongSecret, code); status != 401 || body["error"] != "invalid_client" {
		t.Fatalf("%d %v", status, body)
	}
	wrongVerifier := f
	wrongVerifier.verifier = strings.Repeat("w", 50)
	if status, body := exchange(t, e, wrongVerifier, code); status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("%d %v", status, body)
	}
	status, tok := exchange(t, e, f, code)
	if status != 200 {
		t.Fatalf("the code still works after failed attempts that didn't use it: %d %v", status, tok)
	}
	if status, body := exchange(t, e, f, code); status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("a code works once: %d %v", status, body)
	}
	if mcpStatus(t, e, tok["access_token"].(string)) != 401 {
		t.Fatal("a reused code revokes what it issued")
	}
	code = codeFrom(t, consent(t, e, owner, f, map[string]any{"organization_id": org, "access": "read"}))
	mustExec(t, e, `UPDATE oauth_codes SET expires_at = now() - interval '1 second' WHERE used_at IS NULL`)
	if status, body := exchange(t, e, f, code); status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("an expired code: %d %v", status, body)
	}
	if status, body := tokenRequest(t, e, f, url.Values{"grant_type": {"password"}}); status != 400 || body["error"] != "unsupported_grant_type" {
		t.Fatalf("%d %v", status, body)
	}
}

func TestOAuthPublicClientsAndRevocation(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	f := newFlow(t, e, true, "")
	code := codeFrom(t, consent(t, e, owner, f, map[string]any{"organization_id": org, "access": "read"}))
	status, tok := exchange(t, e, f, code)
	if status != 200 {
		t.Fatalf("a public client relies on PKCE alone: %d %v", status, tok)
	}
	form := url.Values{"token": {tok["refresh_token"].(string)}, "client_id": {f.clientID}}
	res, err := http.PostForm(e.srv.URL+"/oauth/revoke", form)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("%v %v", res.StatusCode, err)
	}
	res.Body.Close()
	if mcpStatus(t, e, tok["access_token"].(string)) != 401 {
		t.Fatal("revoking the refresh token ends the connection")
	}
	// Unknown tokens are not an error (RFC 7009).
	res, _ = http.PostForm(e.srv.URL+"/oauth/revoke", url.Values{"token": {"hkr_unknown"}, "client_id": {f.clientID}})
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("%d", res.StatusCode)
	}

	// A member's connection goes when they leave the organization.
	member, _ := e.register(t, "member@example.com")
	mustExec(t, e, `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@example.com'`, org)
	code = codeFrom(t, consent(t, e, member, f, map[string]any{"organization_id": org, "access": "read"}))
	_, mt := exchange(t, e, f, code)
	if mcpStatus(t, e, mt["access_token"].(string)) != 200 {
		t.Fatal("the member's connection works")
	}
	if apps := owner.call("GET", "/api/v1/oauth/connected-apps", nil).Body["connected_apps"].([]any); len(apps) != 1 || apps[0].(map[string]any)["owner_email"] != "member@example.com" {
		t.Fatalf("the owner sees members' connections: %v", apps)
	}
	owner.call("DELETE", "/api/v1/organizations/"+org+"/members/"+memberID(t, e, "member@example.com"), nil)
	if mcpStatus(t, e, mt["access_token"].(string)) != 401 {
		t.Fatal("removing the member ends their connection")
	}
}

func TestOAuthConsentAsksForATwoFactorCode(t *testing.T) {
	e, clk := newTwoFactorEnv(t)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	secret, _ := enableTwoFactor(t, owner, clk)
	f := newFlow(t, e, false, "read")

	q := strings.TrimPrefix(func() string {
		res, _ := noRedirects.Get(e.srv.URL + "/oauth/authorize?" + f.query().Encode())
		res.Body.Close()
		return res.Header.Get("Location")
	}(), "http://app.test/connect?")
	if info := owner.call("GET", "/api/v1/oauth/authorize?"+q, nil); info.Body["two_factor_code_needed"] != true {
		t.Fatalf("%v", info.Body)
	}
	if r := consent(t, e, owner, f, map[string]any{"organization_id": org, "access": "read"}); r.Status != 400 || errCode(r) != "INVALID_CODE" {
		t.Fatalf("no code: %d %v", r.Status, r.Body)
	}
	code, _ := twofactor.Code(secret, clk.now())
	codeFrom(t, consent(t, e, owner, f, map[string]any{"organization_id": org, "access": "read", "two_factor_code": code}))
}

func TestOAuthClientRedirectURIsAreStrict(t *testing.T) {
	e := newEnv(t, 1000)
	svc := oauth.NewService(e.pool, nil, nil, &email.Memory{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "http://app.test")
	for _, bad := range []string{"https://claude.ai/cb?x=1", "https://claude.ai/cb?", "https://claude.ai/cb#frag", "http://claude.ai/cb", "ftp://claude.ai/cb"} {
		if _, _, err := svc.CreateClient(t.Context(), "Bad", []string{bad}, false); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	if _, _, err := svc.CreateClient(t.Context(), "Local", []string{"http://localhost:6274/oauth/callback"}, true); err != nil {
		t.Fatalf("http on localhost is fine for local tools: %v", err)
	}
}
