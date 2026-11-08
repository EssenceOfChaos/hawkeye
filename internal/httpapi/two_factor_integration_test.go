package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/twofactor"
)

// clock is a test clock the two-factor service reads; tick moves it to the next 30-second step, since each code works only once.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) tick()          { c.t = c.t.Add(31 * time.Second) }

func newTwoFactorEnv(t *testing.T) (*env, *clock) {
	c := &clock{t: time.Now()}
	return newEnvOpts(t, 1000, envOpts{clock: c.now}), c
}

// enableTwoFactor sets up two-factor sign-in for a signed-in client and returns its secret and recovery codes.
func enableTwoFactor(t *testing.T, c *client, clk *clock) (string, []string) {
	t.Helper()
	r := c.call("POST", "/api/v1/account/two-factor/setup", map[string]any{"password": pw})
	if r.Status != 200 {
		t.Fatalf("setup: %d %v", r.Status, r.Body)
	}
	secret := r.Body["setup"].(map[string]any)["secret"].(string)
	code, _ := twofactor.Code(secret, clk.now())
	r = c.call("POST", "/api/v1/account/two-factor/enable", map[string]any{"code": code})
	if r.Status != 200 {
		t.Fatalf("enable: %d %v", r.Status, r.Body)
	}
	clk.tick()
	var codes []string
	for _, x := range r.Body["recovery_codes"].([]any) {
		codes = append(codes, x.(string))
	}
	return secret, codes
}

// signIn logs in with the password and, when asked, a code; it returns the final response.
func signIn(t *testing.T, e *env, addr, password string, code func() string) (*client, resp) {
	t.Helper()
	c := e.newClient(t)
	r := c.call("POST", "/api/v1/auth/login", map[string]any{"email": addr, "password": password})
	if r.Status != 200 || r.Body["two_factor_required"] != true || code == nil {
		return c, r
	}
	return c, c.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": r.Body["challenge"], "code": code()})
}

func countMail(e *env, to, subject string) int {
	n := 0
	for _, m := range e.mail.Messages() {
		if m.To == to && strings.Contains(m.Subject, subject) {
			n++
		}
	}
	return n
}

func TestTwoFactorSetupAndSignIn(t *testing.T) {
	e, clk := newTwoFactorEnv(t)
	owner, _ := e.register(t, "me@example.com")
	if r := owner.call("GET", "/api/v1/account/two-factor", nil); r.Status != 200 || r.Body["two_factor"].(map[string]any)["enabled"] != false {
		t.Fatalf("off by default: %d %v", r.Status, r.Body)
	}

	// Setting it up asks for the password again.
	if r := owner.call("POST", "/api/v1/account/two-factor/setup", map[string]any{"password": "wrong-password-123"}); r.Status != 403 || errCode(r) != "WRONG_PASSWORD" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	r := owner.call("POST", "/api/v1/account/two-factor/setup", map[string]any{"password": pw})
	setup := r.Body["setup"].(map[string]any)
	if !strings.HasPrefix(setup["qr_code"].(string), "data:image/png;base64,") || !strings.HasPrefix(setup["otpauth_uri"].(string), "otpauth://totp/HawkEye:me@example.com?") {
		t.Fatalf("%v", setup)
	}
	if r := owner.call("POST", "/api/v1/account/two-factor/enable", map[string]any{"code": "000000"}); r.Status != 400 || errCode(r) != "INVALID_CODE" {
		t.Fatalf("a wrong code doesn't turn it on: %d %v", r.Status, r.Body)
	}
	secret := setup["secret"].(string)
	code, _ := twofactor.Code(secret, clk.now())
	r = owner.call("POST", "/api/v1/account/two-factor/enable", map[string]any{"code": code})
	if r.Status != 200 || len(r.Body["recovery_codes"].([]any)) != twofactor.RecoveryCodeCount {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	recovery := r.Body["recovery_codes"].([]any)[0].(string)
	clk.tick() // the code just used can't be used again
	if countMail(e, "me@example.com", "two-factor sign-in was turned on") != 1 {
		t.Fatal("no email that it was turned on")
	}
	var sealed string
	e.pool.QueryRow(t.Context(), `SELECT totp_secret FROM users WHERE email = 'me@example.com'`).Scan(&sealed)
	if strings.Contains(sealed, secret) || !strings.HasPrefix(sealed, "enc:") {
		t.Fatal("the secret must be stored encrypted")
	}

	// The password alone no longer signs in: it returns a challenge and no session.
	c, r := signIn(t, e, "me@example.com", pw, nil)
	if r.Status != 200 || r.Body["two_factor_required"] != true || r.Body["challenge"] == "" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if me := c.call("GET", "/api/v1/auth/me", nil); me.Status != 401 {
		t.Fatalf("no session before the code: %d", me.Status)
	}
	challenge := r.Body["challenge"]
	bad := c.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": challenge, "code": "123456"})
	if bad.Status != 401 || errCode(bad) != "INVALID_CODE" || bad.Body["error"].(map[string]any)["details"].(map[string]any)["tries_left"] != float64(4) {
		t.Fatalf("%d %v", bad.Status, bad.Body)
	}
	code, _ = twofactor.Code(secret, clk.now())
	if r := c.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": challenge, "code": code}); r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	me := c.call("GET", "/api/v1/auth/me", nil)
	if me.Status != 200 || me.Body["two_factor"].(map[string]any)["enabled"] != true {
		t.Fatalf("%d %v", me.Status, me.Body)
	}

	// The same code can't be used again, even on a new challenge in the same 30 seconds.
	if _, r := signIn(t, e, "me@example.com", pw, func() string { return code }); r.Status != 401 || errCode(r) != "INVALID_CODE" {
		t.Fatalf("a code must work only once: %d %v", r.Status, r.Body)
	}

	// A recovery code signs in once, however it is typed, and the person is told.
	typed := strings.ToUpper(strings.ReplaceAll(recovery, "-", " "))
	if _, r := signIn(t, e, "me@example.com", pw, func() string { return typed }); r.Status != 200 || r.Body["used_recovery_code"] != true {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if countMail(e, "me@example.com", "recovery code was used") != 1 {
		t.Fatal("no email about the recovery code")
	}
	if _, r := signIn(t, e, "me@example.com", pw, func() string { return recovery }); r.Status != 401 {
		t.Fatalf("a recovery code works once: %d", r.Status)
	}
	if st := owner.call("GET", "/api/v1/account/two-factor", nil).Body["two_factor"].(map[string]any); st["recovery_codes_left"] != float64(9) {
		t.Fatalf("%v", st)
	}
	// A wrong password still says only that the credentials are wrong.
	if _, r := signIn(t, e, "me@example.com", "not-the-password", nil); r.Status != 401 || errCode(r) != "INVALID_CREDENTIALS" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestTwoFactorChallengeIsShortLivedAndLimited(t *testing.T) {
	e, clk := newTwoFactorEnv(t)
	owner, _ := e.register(t, "me@example.com")
	secret, _ := enableTwoFactor(t, owner, clk)

	c := e.newClient(t)
	challenge := c.call("POST", "/api/v1/auth/login", map[string]any{"email": "me@example.com", "password": pw}).Body["challenge"].(string)
	var stored int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM login_challenges WHERE position($1::bytea in token_hash) > 0`, []byte(challenge)).Scan(&stored)
	if stored != 0 {
		t.Fatal("only a hash of the challenge is stored")
	}
	for i := 0; i < 4; i++ {
		if r := c.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": challenge, "code": "000000"}); errCode(r) != "INVALID_CODE" {
			t.Fatalf("try %d: %v", i, r.Body)
		}
	}
	if r := c.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": challenge, "code": "000000"}); r.Status != 401 || errCode(r) != "SIGN_IN_EXPIRED" {
		t.Fatalf("the fifth wrong code uses the challenge up: %d %v", r.Status, r.Body)
	}
	code, _ := twofactor.Code(secret, clk.now())
	if r := c.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": challenge, "code": code}); errCode(r) != "SIGN_IN_EXPIRED" {
		t.Fatalf("even the right code can't use it after that: %v", r.Body)
	}

	challenge = c.call("POST", "/api/v1/auth/login", map[string]any{"email": "me@example.com", "password": pw}).Body["challenge"].(string)
	mustExec(t, e, `UPDATE login_challenges SET expires_at = now() - interval '1 second'`)
	if r := c.call("POST", "/api/v1/auth/login/two-factor", map[string]any{"challenge": challenge, "code": code}); errCode(r) != "SIGN_IN_EXPIRED" {
		t.Fatalf("an expired challenge: %v", r.Body)
	}
}

func TestTwoFactorRecoveryCodesAndTurningItOff(t *testing.T) {
	e, clk := newTwoFactorEnv(t)
	owner, _ := e.register(t, "me@example.com")
	secret, old := enableTwoFactor(t, owner, clk)

	code, _ := twofactor.Code(secret, clk.now())
	r := owner.call("POST", "/api/v1/account/two-factor/recovery-codes", map[string]any{"code": code})
	if r.Status != 200 || len(r.Body["recovery_codes"].([]any)) != 10 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	clk.tick()
	if _, r := signIn(t, e, "me@example.com", pw, func() string { return old[0] }); r.Status != 401 {
		t.Fatalf("old recovery codes stop working: %d", r.Status)
	}

	// Turning it off needs the password and a code.
	code, _ = twofactor.Code(secret, clk.now())
	if r := owner.call("POST", "/api/v1/account/two-factor/disable", map[string]any{"password": "wrong-password-123", "code": code}); r.Status != 403 {
		t.Fatalf("%d", r.Status)
	}
	if r := owner.call("POST", "/api/v1/account/two-factor/disable", map[string]any{"password": pw, "code": "000000"}); errCode(r) != "INVALID_CODE" {
		t.Fatalf("%v", r.Body)
	}
	if r := owner.call("POST", "/api/v1/account/two-factor/disable", map[string]any{"password": pw, "code": code}); r.Status != 204 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if countMail(e, "me@example.com", "turned off") != 1 {
		t.Fatal("no email that it was turned off")
	}
	if _, r := signIn(t, e, "me@example.com", pw, nil); r.Status != 200 || r.Body["two_factor_required"] == true {
		t.Fatalf("the password alone signs in again: %v", r.Body)
	}
}

func TestOwnersCanRequireTwoFactorForTheirOrganization(t *testing.T) {
	e, clk := newTwoFactorEnv(t)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "agency")
	member, _ := e.register(t, "member@example.com")
	e.markVerified(t, "member@example.com")
	mustExec(t, e, `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@example.com'`, org)
	path := "/api/v1/organizations/" + org + "/two-factor"

	// Off by default; only an owner who uses it can turn it on.
	if r := owner.call("GET", path, nil); r.Status != 200 || r.Body["required"] != false {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := owner.call("PUT", path, map[string]any{"required": true}); r.Status != 409 || errCode(r) != "OWNER_NEEDS_TWO_FACTOR" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := member.call("PUT", path, map[string]any{"required": true}); r.Status != 403 && r.Status != 404 {
		t.Fatalf("a member can't: %d", r.Status)
	}
	ownerSecret, _ := enableTwoFactor(t, owner, clk)
	r := owner.call("PUT", path, map[string]any{"required": true})
	if r.Status != 200 || r.Body["required"] != true {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if countMail(e, "member@example.com", "requires two-factor sign-in") != 1 || countMail(e, "owner@example.com", "requires two-factor") != 0 {
		t.Fatal("members without it are emailed, the owner is not")
	}
	owner.call("PUT", path, map[string]any{"required": true})
	if countMail(e, "member@example.com", "requires two-factor sign-in") != 1 {
		t.Fatal("turning it on again doesn't email again")
	}

	// During the grace period the member keeps working, and is told the date.
	kr := member.callIn(org, "POST", "/api/v1/api-keys", map[string]any{"name": "Member script", "scope": "read"})
	if kr.Status != 201 {
		t.Fatalf("%d %v", kr.Status, kr.Body)
	}
	key := kr.Body["token"].(string)
	if r := member.callIn(org, "GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if o := orgIn(t, member, org); o["require_two_factor"] != true || o["two_factor_grace_ends_at"] == nil || o["two_factor_blocked"] != false {
		t.Fatalf("%v", o)
	}

	// After it, the organization is closed to them and to their keys, but not their own organization or their account.
	mustExec(t, e, `UPDATE organizations SET require_two_factor_since = now() - interval '8 days' WHERE id = $1`, org)
	mustExec(t, e, `UPDATE organization_members SET created_at = now() - interval '30 days' WHERE organization_id = $1`, org)
	clk.t = time.Now()
	if r := member.callIn(org, "GET", "/api/v1/monitors", nil); r.Status != 403 || errCode(r) != "TWO_FACTOR_REQUIRED" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := e.keyClient(t, key).call("GET", "/api/v1/monitors", nil); r.Status != 403 || errCode(r) != "TWO_FACTOR_REQUIRED" {
		t.Fatalf("the member's key: %d %v", r.Status, r.Body)
	}
	mcpReq, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Accept", "application/json, text/event-stream")
	mcpReq.Header.Set("Authorization", "Bearer "+key)
	if res, err := http.DefaultClient.Do(mcpReq); err != nil || res.StatusCode != 403 {
		t.Fatalf("the member's key on the MCP server: %v %v", res.StatusCode, err)
	}
	if r := member.call("GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("their own organization still works: %d", r.Status)
	}
	if o := orgIn(t, member, org); o["two_factor_blocked"] != true {
		t.Fatalf("%v", o)
	}
	if r := owner.call("GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("the owner (who uses it) is unaffected: %d", r.Status)
	}

	// The owner can't hand the organization to someone shut out of it, or turn their own off while requiring it.
	memberID := memberID(t, e, "member@example.com")
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/transfer", map[string]any{"user_id": memberID, "confirm_name": "owner's organization"}); errCode(r) != "NEW_OWNER_NEEDS_TWO_FACTOR" {
		t.Fatalf("%v", r.Body)
	}
	code, _ := twofactor.Code(ownerSecret, clk.now())
	if r := owner.call("POST", "/api/v1/account/two-factor/disable", map[string]any{"password": pw, "code": code}); errCode(r) != "TWO_FACTOR_REQUIRED_BY_ORGANIZATION" {
		t.Fatalf("%v", r.Body)
	}

	// Setting it up opens the organization again, for the member and their key.
	enableTwoFactor(t, member, clk)
	if r := member.callIn(org, "GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := e.keyClient(t, key).call("GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("%d", r.Status)
	}
	members := owner.call("GET", path, nil).Body["members"].([]any)
	for _, m := range members {
		if m.(map[string]any)["two_factor_enabled"] != true {
			t.Fatalf("%v", members)
		}
	}

	// Someone who joins later gets their own grace period, and turning the requirement off ends it for everyone.
	late, _ := e.register(t, "late@example.com")
	mustExec(t, e, `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'late@example.com'`, org)
	if r := late.callIn(org, "GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("a new member has 7 days too: %d %v", r.Status, r.Body)
	}
	if r := owner.call("PUT", path, map[string]any{"required": false}); r.Status != 200 || r.Body["required"] != false {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func orgIn(t *testing.T, c *client, org string) map[string]any {
	t.Helper()
	for _, o := range c.call("GET", "/api/v1/auth/me", nil).Body["organizations"].([]any) {
		if o.(map[string]any)["id"] == org {
			return o.(map[string]any)
		}
	}
	t.Fatalf("not a member of %s", org)
	return nil
}
