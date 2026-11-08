package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

// keyClient sends requests the way a script does: an API key in the Authorization header, no cookies and no CSRF token.
type keyClient struct {
	t     *testing.T
	base  string
	token string
}

func (e *env) keyClient(t *testing.T, token string) *keyClient {
	return &keyClient{t: t, base: e.srv.URL, token: token}
}

func (k *keyClient) send(method, path string, body any, headers map[string]string) (resp, http.Header) {
	k.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, k.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+k.token)
	for h, v := range headers {
		req.Header.Set(h, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		k.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return resp{res.StatusCode, m}, res.Header
}

func (k *keyClient) call(method, path string, body any) resp {
	k.t.Helper()
	r, _ := k.send(method, path, body, nil)
	return r
}

// markVerified marks an address verified (creating keys needs a verified address).
func (e *env) markVerified(t *testing.T, addr string) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `UPDATE users SET email_verified_at = now() WHERE email = $1`, addr); err != nil {
		t.Fatal(err)
	}
}

func (e *env) setOrgPlan(t *testing.T, org, code string) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = $2) WHERE organization_id = $1`, org, code); err != nil {
		t.Fatal(err)
	}
}

// newKey creates a key through the API as c and returns the key and its token.
func newKey(t *testing.T, c *client, body map[string]any) (map[string]any, string) {
	t.Helper()
	r := c.call("POST", "/api/v1/api-keys", body)
	if r.Status != 201 {
		t.Fatalf("create key %v: %d %v", body, r.Status, r.Body)
	}
	return r.Body["key"].(map[string]any), r.Body["token"].(string)
}

func TestAPIKeyLifecycle(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	owner.call("POST", "/api/v1/monitors", monitorBody("Website"))

	if r := owner.call("POST", "/api/v1/api-keys", map[string]any{"name": "Dashboard", "scope": "read"}); r.Status != 403 || errCode(r) != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("an unverified address can't make keys: %d %v", r.Status, r.Body)
	}
	e.markVerified(t, "owner@example.com")

	before := time.Now()
	key, token := newKey(t, owner, map[string]any{"name": "  Dashboard  ", "scope": "read"})
	if !strings.HasPrefix(token, "hk_") || len(token) != 46 || key["prefix"] != token[:10] || key["name"] != "Dashboard" || key["scope"] != "read" {
		t.Fatalf("token %q, key %v", token, key)
	}
	// 90 days unless chosen otherwise.
	exp, _ := time.Parse(time.RFC3339, key["expires_at"].(string))
	if d := exp.Sub(before); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatalf("default expiry %v from now", d)
	}

	// Only a hash is stored, and the listing never includes the token.
	var stored int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM api_tokens WHERE position($1::bytea in token_hash) > 0`, []byte(token)).Scan(&stored)
	if stored != 0 {
		t.Fatal("the raw token must not be stored")
	}
	list := owner.call("GET", "/api/v1/api-keys", nil)
	b, _ := json.Marshal(list.Body)
	if list.Status != 200 || strings.Contains(string(b), token) || len(list.Body["keys"].([]any)) != 1 {
		t.Fatalf("%d %s", list.Status, b)
	}

	// The owner is told a key was made.
	var notice bool
	for _, m := range e.mail.Messages() {
		if m.To == "owner@example.com" && strings.Contains(m.Subject, "API key was created") && strings.Contains(m.Text, "Dashboard") && strings.Contains(m.Text, "Read only") {
			notice = true
		}
	}
	if !notice {
		t.Fatal("no key-created email")
	}

	// A script uses it with no cookie and no CSRF token.
	k := e.keyClient(t, token)
	if r := k.call("GET", "/api/v1/monitors", nil); r.Status != 200 || len(r.Body["monitors"].([]any)) != 1 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	var lastUsed *time.Time
	e.pool.QueryRow(t.Context(), `SELECT last_used_at FROM api_tokens`).Scan(&lastUsed)
	if lastUsed == nil {
		t.Fatal("last_used_at not recorded")
	}

	// Read-only keys can't change anything.
	if r := k.call("POST", "/api/v1/monitors", monitorBody("Another")); r.Status != 403 || errCode(r) != "TOKEN_SCOPE" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}

	// Revoked keys stop at once.
	if r := owner.call("DELETE", "/api/v1/api-keys/"+key["id"].(string), nil); r.Status != 204 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	r, h := k.send("GET", "/api/v1/monitors", nil, nil)
	if r.Status != 401 || errCode(r) != "UNAUTHENTICATED" || !strings.HasPrefix(h.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("%d %v %v", r.Status, r.Body, h)
	}
	if r := owner.call("DELETE", "/api/v1/api-keys/"+key["id"].(string), nil); r.Status != 404 {
		t.Fatalf("revoking twice: %d", r.Status)
	}
	if got := owner.call("GET", "/api/v1/api-keys", nil).Body["keys"].([]any); len(got) != 0 {
		t.Fatalf("revoked keys aren't listed: %v", got)
	}

	// Made-up and malformed credentials.
	for _, bad := range []string{"hk_" + strings.Repeat("A", 43), "not-a-key", strings.Repeat("x", 500)} {
		if r := e.keyClient(t, bad).call("GET", "/api/v1/monitors", nil); r.Status != 401 {
			t.Fatalf("%q: %d", bad, r.Status)
		}
	}
}

func TestAPIKeyWritesNeedAPaidPlan(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")

	r := owner.call("POST", "/api/v1/api-keys", map[string]any{"name": "Terraform", "scope": "write"})
	if r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" || r.Body["error"].(map[string]any)["details"].(map[string]any)["limit_type"] != "api_write" {
		t.Fatalf("Free can't make write keys: %d %v", r.Status, r.Body)
	}
	_, readToken := newKey(t, owner, map[string]any{"name": "Read", "scope": "read"})

	e.setOrgPlan(t, org, "pro")
	_, token := newKey(t, owner, map[string]any{"name": "Terraform", "scope": "write"})
	k := e.keyClient(t, token)
	created := k.call("POST", "/api/v1/monitors", monitorBody("From a script"))
	if created.Status != 201 {
		t.Fatalf("%d %v", created.Status, created.Body)
	}
	id := created.Body["monitor"].(map[string]any)["id"].(string)
	if r := k.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"name": "Renamed"}); r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}

	// After a downgrade the write key keeps reading but stops writing, and works again after an upgrade.
	e.setOrgPlan(t, org, "free")
	if r := k.call("POST", "/api/v1/monitors/"+id+"/pause", nil); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := k.call("GET", "/api/v1/monitors/"+id, nil); r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := e.keyClient(t, readToken).call("GET", "/api/v1/dashboard/summary", nil); r.Status != 200 {
		t.Fatalf("reading works on Free: %d %v", r.Status, r.Body)
	}
	e.setOrgPlan(t, org, "agency")
	if r := k.call("POST", "/api/v1/monitors/"+id+"/pause", nil); r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestAPIKeysCannotReachSessionOnlyRoutes(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "agency")
	_, token := newKey(t, owner, map[string]any{"name": "Everything", "scope": "write"})
	k := e.keyClient(t, token)

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/auth/me"},
		{"POST", "/api/v1/auth/change-password"},
		{"GET", "/api/v1/account/export"},
		{"POST", "/api/v1/account/delete"},
		{"GET", "/api/v1/organizations"},
		{"POST", "/api/v1/organizations/" + org + "/invitations"},
		{"POST", "/api/v1/organizations/" + org + "/transfer"},
		{"GET", "/api/v1/api-keys"},
		{"POST", "/api/v1/api-keys"},
		{"POST", "/api/v1/billing/checkout-session"},
		{"POST", "/api/v1/billing/cancel-subscription"},
		{"GET", "/api/v1/notification-settings"},
		{"POST", "/api/v1/notification-settings"},
	} {
		if r := k.call(c.method, c.path, map[string]any{"name": "x", "scope": "write"}); r.Status != 403 || errCode(r) != "SESSION_REQUIRED" {
			t.Fatalf("%s %s with a key: %d %v", c.method, c.path, r.Status, r.Body)
		}
	}

	// A request with an Authorization header never falls back to the session cookie, so it can't skip the CSRF check with a cookie either.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/monitors", strings.NewReader(`{"name":"x","url":"https://example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer not-a-real-key")
	res, err := owner.http.Do(req) // carries the owner's session cookie
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("a cookie plus a bad key: %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", e.srv.URL+"/api/v1/account/delete", strings.NewReader(`{"password":"`+pw+`"}`))
	req.Header.Set("Authorization", "Basic eDp5")
	res, err = owner.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("a cookie plus another Authorization scheme on a session route: %d", res.StatusCode)
	}
	if r := owner.call("GET", "/api/v1/auth/me", nil); r.Status != 200 {
		t.Fatalf("the account must still exist: %d", r.Status)
	}
}

func TestAPIKeysFollowTheirOwnersRoleAndMembership(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "agency")
	member, _ := e.register(t, "member@example.com")
	e.markVerified(t, "member@example.com")
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@example.com'`, org)

	r := member.callIn(org, "POST", "/api/v1/api-keys", map[string]any{"name": "Member script", "scope": "write"})
	if r.Status != 201 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	memberKey := r.Body["key"].(map[string]any)["id"].(string)
	k := e.keyClient(t, r.Body["token"].(string))
	_, ownerToken := newKey(t, owner, map[string]any{"name": "Owner script", "scope": "read"})

	// The key acts in its own organization (not the member's personal one) and with the member's role.
	if r := k.call("POST", "/api/v1/monitors", monitorBody("Team site")); r.Status != 201 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if n := len(owner.call("GET", "/api/v1/monitors", nil).Body["monitors"].([]any)); n != 1 {
		t.Fatalf("the monitor should be in the team organization: %d", n)
	}
	page := map[string]any{"slug": "team", "title": "Team", "published": true, "monitors": []any{}}
	if r := k.call("POST", "/api/v1/status-pages", page); r.Status != 403 || errCode(r) != "FORBIDDEN" {
		t.Fatalf("owner-only routes still need the owner: %d %v", r.Status, r.Body)
	}

	// Members see and revoke only their own keys; the owner sees everyone's.
	if keys := member.callIn(org, "GET", "/api/v1/api-keys", nil).Body["keys"].([]any); len(keys) != 1 || keys[0].(map[string]any)["mine"] != true {
		t.Fatalf("%v", keys)
	}
	ownerView := owner.call("GET", "/api/v1/api-keys", nil).Body["keys"].([]any)
	if len(ownerView) != 2 {
		t.Fatalf("%v", ownerView)
	}
	for _, x := range ownerView {
		if x.(map[string]any)["owner_email"] == "owner@example.com" {
			if r := member.callIn(org, "DELETE", "/api/v1/api-keys/"+x.(map[string]any)["id"].(string), nil); r.Status != 404 {
				t.Fatalf("a member can't revoke the owner's key: %d", r.Status)
			}
		}
	}

	// A key always names its own organization; a different X-Organization-ID is refused.
	_, personal := e.register(t, "other@example.com")
	if r, _ := k.send("GET", "/api/v1/monitors", nil, map[string]string{"X-Organization-ID": personal}); r.Status != 403 || errCode(r) != "TOKEN_ORGANIZATION_MISMATCH" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r, _ := k.send("GET", "/api/v1/monitors", nil, map[string]string{"X-Organization-ID": org}); r.Status != 200 {
		t.Fatalf("naming its own organization is fine: %d", r.Status)
	}

	// Removing the member removes their keys with them.
	if r := owner.call("DELETE", "/api/v1/organizations/"+org+"/members/"+memberID(t, e, "member@example.com"), nil); r.Status != 204 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := k.call("GET", "/api/v1/monitors", nil); r.Status != 401 {
		t.Fatalf("a removed member's key: %d", r.Status)
	}
	var left int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM api_tokens WHERE id = $1`, memberKey).Scan(&left)
	if left != 0 {
		t.Fatal("the removed member's key should be deleted")
	}
	if r := e.keyClient(t, ownerToken).call("GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("the owner's key is unaffected: %d", r.Status)
	}
}

func memberID(t *testing.T, e *env, addr string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(t.Context(), `SELECT id::text FROM users WHERE email = $1`, addr).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAPIKeyExpiryChoices(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	now := time.Now().UTC()

	for _, bad := range []map[string]any{
		{"name": "", "scope": "read"},
		{"name": strings.Repeat("n", 101), "scope": "read"},
		{"name": "x", "scope": "admin"},
		{"name": "x", "scope": "read", "expires_at": now.Add(-time.Hour)},
		{"name": "x", "scope": "read", "expires_at": now.Add(10 * time.Minute)},
		{"name": "x", "scope": "read", "expires_at": now.Add(400 * 24 * time.Hour)},
		{"name": "x", "scope": "read", "expires_at": now.Add(48 * time.Hour), "no_expiry": true},
	} {
		if r := owner.call("POST", "/api/v1/api-keys", bad); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
			t.Fatalf("%v: %d %v", bad, r.Status, r.Body)
		}
	}

	forever, _ := newKey(t, owner, map[string]any{"name": "Terraform", "scope": "read", "no_expiry": true})
	if forever["expires_at"] != nil {
		t.Fatalf("%v", forever)
	}
	chosen := now.Add(30 * 24 * time.Hour).Truncate(time.Second)
	short, token := newKey(t, owner, map[string]any{"name": "Short", "scope": "read", "expires_at": chosen})
	if got, _ := time.Parse(time.RFC3339, short["expires_at"].(string)); !got.Equal(chosen) {
		t.Fatalf("expires_at %v, want %v", got, chosen)
	}

	// Past its date, a key says it expired (not just "invalid"), and the listing marks it.
	mustExec(t, e, `UPDATE api_tokens SET created_at = now() - interval '31 days', expires_at = now() - interval '1 minute' WHERE id = $1`, short["id"])
	if r := e.keyClient(t, token).call("GET", "/api/v1/monitors", nil); r.Status != 401 || errCode(r) != "TOKEN_EXPIRED" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	for _, x := range owner.call("GET", "/api/v1/api-keys", nil).Body["keys"].([]any) {
		k := x.(map[string]any)
		if k["id"] == short["id"] && k["expired"] != true {
			t.Fatalf("%v", k)
		}
	}
}

func TestAPIKeyLimitPerOrganization(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	// Revoked and expired keys don't count.
	mustExec(t, e, `INSERT INTO api_tokens (organization_id, user_id, name, scope, prefix, token_hash, revoked_at)
		SELECT $1, u.id, 'old', 'read', 'hk_old', sha256(gen_random_uuid()::text::bytea), now() FROM users u WHERE u.email = 'owner@example.com'`, org)
	mustExec(t, e, `INSERT INTO api_tokens (organization_id, user_id, name, scope, prefix, token_hash, created_at, expires_at)
		SELECT $1, u.id, 'gone', 'read', 'hk_gone', sha256(gen_random_uuid()::text::bytea), now() - interval '2 days', now() - interval '1 day' FROM users u WHERE u.email = 'owner@example.com'`, org)
	for i := 0; i < 25; i++ {
		newKey(t, owner, map[string]any{"name": "Key", "scope": "read"})
	}
	if r := owner.call("POST", "/api/v1/api-keys", map[string]any{"name": "One too many", "scope": "read"}); r.Status != 403 || errCode(r) != "API_KEYS_LIMIT" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestPasswordResetRevokesAPIKeys(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	_, token := newKey(t, owner, map[string]any{"name": "Script", "scope": "read"})

	// Changing the password while signed in keeps keys working.
	if r := owner.call("POST", "/api/v1/auth/change-password", map[string]any{"current_password": pw, "new_password": "another-long-password"}); r.Status != 204 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := e.keyClient(t, token).call("GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("%d", r.Status)
	}

	anon := e.newClient(t)
	anon.call("POST", "/api/v1/auth/forgot-password", map[string]any{"email": "owner@example.com"})
	reset := e.lastToken(t, "owner@example.com", "Reset")
	if r := anon.call("POST", "/api/v1/auth/reset-password", map[string]any{"token": reset, "password": "a-third-long-password"}); r.Status != 204 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := e.keyClient(t, token).call("GET", "/api/v1/monitors", nil); r.Status != 401 {
		t.Fatalf("a reset revokes keys: %d", r.Status)
	}
}

func TestAPIKeysTurnedOff(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{noAPIKeys: true})
	owner, _ := e.register(t, "owner@example.com")
	if r := owner.call("GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("sessions keep working: %d", r.Status)
	}
	if r := owner.call("GET", "/api/v1/api-keys", nil); r.Status != 503 || errCode(r) != "API_KEYS_DISABLED" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := e.keyClient(t, "hk_"+strings.Repeat("A", 43)).call("GET", "/api/v1/monitors", nil); r.Status != 503 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestAPIKeyRateLimits(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{shared: true})
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	_, token := newKey(t, owner, map[string]any{"name": "Busy", "scope": "write"})
	k := e.keyClient(t, token)
	var limited resp
	var header http.Header
	for i := 0; i < 31; i++ {
		limited, header = k.send("POST", "/api/v1/tags", map[string]any{"name": "t" + strings.Repeat("x", i)}, nil)
	}
	if limited.Status != 429 || header.Get("Retry-After") == "" {
		t.Fatalf("the 31st write in a minute: %d %v", limited.Status, limited.Body)
	}
	if r := k.call("GET", "/api/v1/tags", nil); r.Status != 200 {
		t.Fatalf("reads have their own, larger budget: %d", r.Status)
	}
}

// Every /api/v1 route is either open to API keys on purpose, public, or session-only. A new route must be added to one of these lists, and the
// test checks the server agrees: keys reach exactly the routes listed here.
func TestEveryRouteIsClassifiedForAPIKeys(t *testing.T) {
	tokenRoutes := setOf(
		"GET /api/v1/usage", "GET /api/v1/monitors", "POST /api/v1/monitors", "POST /api/v1/monitors/bulk",
		"GET /api/v1/tags", "POST /api/v1/tags", "PATCH /api/v1/tags/:tagID", "DELETE /api/v1/tags/:tagID",
		"GET /api/v1/monitors/:monitorID", "PATCH /api/v1/monitors/:monitorID", "DELETE /api/v1/monitors/:monitorID",
		"POST /api/v1/monitors/:monitorID/pause", "POST /api/v1/monitors/:monitorID/resume",
		"POST /api/v1/monitors/:monitorID/badge", "DELETE /api/v1/monitors/:monitorID/badge",
		"GET /api/v1/monitors/:monitorID/pings", "POST /api/v1/monitors/:monitorID/ping-token",
		"GET /api/v1/monitors/:monitorID/incidents", "GET /api/v1/incidents", "GET /api/v1/incidents/:incidentID/updates",
		"POST /api/v1/incidents/:incidentID/updates", "DELETE /api/v1/incidents/:incidentID/updates/:updateID",
		"GET /api/v1/billing/subscription",
		"GET /api/v1/branding", "PUT /api/v1/branding", "GET /api/v1/branding/logo", "PUT /api/v1/branding/logo", "DELETE /api/v1/branding/logo",
		"GET /api/v1/monitors/:monitorID/report.pdf",
		"GET /api/v1/monitors/:monitorID/results", "GET /api/v1/monitors/:monitorID/uptime", "GET /api/v1/monitors/:monitorID/response-times",
		"GET /api/v1/dashboard/summary", "GET /api/v1/dashboard/recent-incidents", "GET /api/v1/dashboard/response-times",
		"GET /api/v1/regions", "GET /api/v1/monitors/:monitorID/regions",
		"GET /api/v1/status-pages", "POST /api/v1/status-pages", "GET /api/v1/status-pages/:pageID", "PUT /api/v1/status-pages/:pageID",
		"DELETE /api/v1/status-pages/:pageID", "GET /api/v1/status-pages/:pageID/logo", "PUT /api/v1/status-pages/:pageID/logo",
		"DELETE /api/v1/status-pages/:pageID/logo",
		"GET /api/v1/maintenance-windows", "POST /api/v1/maintenance-windows", "DELETE /api/v1/maintenance-windows/:windowID",
		"GET /api/v1/domains", "POST /api/v1/domains", "DELETE /api/v1/domains/:domainID",
		"GET /api/v1/notification-settings/coverage",
	)
	// Public: no sign-in at all, so an API key changes nothing.
	publicRoutes := setOf(
		"POST /api/v1/auth/register", "POST /api/v1/auth/login", "POST /api/v1/auth/login/two-factor", "POST /api/v1/auth/forgot-password", "POST /api/v1/auth/reset-password",
		"POST /api/v1/auth/verify-email", "POST /api/v1/auth/logout", "GET /api/v1/plans", "GET /api/v1/public/status/:slug",
		"GET /api/v1/public/status/:slug/logo", "POST /api/v1/tools/ssl-check", "POST /api/v1/tools/site-check", "GET /api/v1/contact/categories",
		"POST /api/v1/contact", "POST /api/v1/waitlist", "POST /api/v1/waitlist/confirm", "POST /api/v1/waitlist/leave", "POST /api/v1/notification-settings/confirm", "POST /api/v1/lifecycle/unsubscribe", "POST /api/v1/invitations/preview",
		"POST /api/v1/webhooks/stripe",
	)

	e := newEnv(t, 100000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "agency")
	_, token := newKey(t, owner, map[string]any{"name": "Everything", "scope": "write"})
	k := e.keyClient(t, token)

	seen := 0
	for _, rt := range e.app.Router().Routes() {
		if !strings.HasPrefix(rt.Path, "/api/v1/") || strings.HasSuffix(rt.Path, "*") || rt.Method == echo.RouteNotFound {
			continue
		}
		name := rt.Method + " " + rt.Path
		path := rt.Path
		for _, p := range rt.Parameters {
			path = strings.Replace(path, ":"+p, "00000000-0000-0000-0000-000000000000", 1)
		}
		switch {
		case tokenRoutes[name]:
			seen++
			if r := k.call(rt.Method, path, map[string]any{}); r.Status == 401 || errCode(r) == "SESSION_REQUIRED" || errCode(r) == "CSRF_INVALID" {
				t.Errorf("%s should be open to API keys: %d %v", name, r.Status, r.Body)
			}
		case publicRoutes[name]:
		default:
			if r := k.call(rt.Method, path, map[string]any{}); r.Status != 403 || errCode(r) != "SESSION_REQUIRED" {
				t.Errorf("%s is not listed as open to API keys or public, so a key must get SESSION_REQUIRED: %d %v", name, r.Status, r.Body)
			}
		}
	}
	if seen != len(tokenRoutes) {
		t.Errorf("only %d of the %d routes listed as open to API keys exist; remove the ones that are gone", seen, len(tokenRoutes))
	}
}

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func mustExec(t *testing.T, e *env, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
