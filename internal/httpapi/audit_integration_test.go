package httpapi_test

import (
	"fmt"
	"strings"
	"testing"

	promtest "github.com/prometheus/client_golang/prometheus/testutil"

	"hawkeye/internal/metrics"
)

// logOf reads a security log page as c and fails unless it answers 200.
func logOf(t *testing.T, c *client, path string) []map[string]any {
	t.Helper()
	r := c.call("GET", path, nil)
	if r.Status != 200 {
		t.Fatalf("GET %s: %d %v", path, r.Status, r.Body)
	}
	var out []map[string]any
	for _, ev := range r.Body["events"].([]any) {
		out = append(out, ev.(map[string]any))
	}
	return out
}

func actions(events []map[string]any) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev["action"].(string)
	}
	return out
}

// find returns the newest event with the action, or fails.
func find(t *testing.T, events []map[string]any, action string) map[string]any {
	t.Helper()
	for _, ev := range events {
		if ev["action"] == action {
			return ev
		}
	}
	t.Fatalf("no %s event in %v", action, actions(events))
	return nil
}

func TestAccountLogRecordsSignInsFailuresAndPasswordChanges(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "alice@example.com")

	// A wrong password for a real account is recorded in that account's log; one for an address nobody has is not stored anywhere.
	if r := e.newClient(t).call("POST", "/api/v1/auth/login", map[string]string{"email": "alice@example.com", "password": "wrong-password-here"}); r.Status != 401 {
		t.Fatalf("wrong password: %d", r.Status)
	}
	if r := e.newClient(t).call("POST", "/api/v1/auth/login", map[string]string{"email": "nobody@example.com", "password": "wrong-password-here"}); r.Status != 401 {
		t.Fatalf("unknown account: %d", r.Status)
	}
	if n := e.countRows(t, `SELECT count(*) FROM audit_events WHERE details::text LIKE '%nobody%' OR actor_email = 'nobody@example.com'`); n != 0 {
		t.Fatalf("a sign-in attempt for an unknown address was stored: %d rows", n)
	}
	other := e.newClient(t)
	if r := other.call("POST", "/api/v1/auth/login", map[string]string{"email": "alice@example.com", "password": pw}); r.Status != 200 {
		t.Fatalf("login: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/auth/change-password", map[string]string{"current_password": pw, "new_password": "another-long-password"}); r.Status != 204 {
		t.Fatalf("change password: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/auth/logout", nil); r.Status != 204 {
		t.Fatalf("logout: %d", r.Status)
	}
	c = e.newClient(t)
	if r := c.call("POST", "/api/v1/auth/login", map[string]string{"email": "alice@example.com", "password": "another-long-password"}); r.Status != 200 {
		t.Fatalf("login again: %d", r.Status)
	}

	events := logOf(t, c, "/api/v1/account/security-log")
	want := []string{"account.signed_in", "account.signed_out", "account.password_changed", "account.signed_in", "account.sign_in_failed", "account.signed_up"}
	if got := strings.Join(actions(events), ","); got != strings.Join(want, ",") {
		t.Fatalf("account log, newest first:\n got %s\nwant %s", got, strings.Join(want, ","))
	}
	failed := find(t, events, "account.sign_in_failed")
	if failed["actor"].(map[string]any)["type"] != "anonymous" || failed["details"].(map[string]any)["reason"] != "password" {
		t.Fatalf("failed sign-in: %v", failed)
	}
	if failed["ip"] == nil || failed["user_agent"] == nil || failed["request_id"] == nil {
		t.Fatalf("an event says where the request came from: %v", failed)
	}
	if find(t, events, "account.password_changed")["details"].(map[string]any)["other_sessions_ended"].(float64) != 1 {
		t.Fatalf("the other session was signed out: %v", find(t, events, "account.password_changed"))
	}

	// The account's own log is part of its data export, and the export itself is recorded.
	ex := c.call("GET", "/api/v1/account/export", nil)
	if ex.Status != 200 || len(ex.Body["security_log"].([]any)) != len(events) {
		t.Fatalf("export: %d, %d log events, want %d", ex.Status, len(ex.Body["security_log"].([]any)), len(events))
	}
	if got := actions(logOf(t, c, "/api/v1/account/security-log?limit=1")); got[0] != "account.data_exported" {
		t.Fatalf("after an export: %v", got)
	}

	// One person never sees another's account log.
	bob, _ := e.register(t, "bob@example.com")
	if got := actions(logOf(t, bob, "/api/v1/account/security-log")); len(got) != 1 || got[0] != "account.signed_up" {
		t.Fatalf("bob's log: %v", got)
	}
}

func TestOrganizationLogSaysWhoChangedWhatAndWithWhichCredential(t *testing.T) {
	m := metrics.NewAPI()
	e := newEnvOpts(t, 100000, envOpts{metrics: m})
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "agency")

	// By the owner, signed in.
	r := owner.call("POST", "/api/v1/monitors", map[string]any{"name": "Shop", "url": "https://shop.example.com"})
	if r.Status != 201 {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	mon := r.Body["monitor"].(map[string]any)["id"].(string)
	if r := owner.call("PATCH", "/api/v1/monitors/"+mon, map[string]any{"name": "Shop front", "check_interval_seconds": 300}); r.Status != 200 {
		t.Fatalf("update: %d %v", r.Status, r.Body)
	}

	// By a script with the owner's key.
	key, token := newKey(t, owner, map[string]any{"name": "Deploy script", "scope": "write"})
	k := e.keyClient(t, token)
	if r := k.call("POST", "/api/v1/monitors/"+mon+"/pause", nil); r.Status != 200 {
		t.Fatalf("pause with key: %d %v", r.Status, r.Body)
	}

	// By Claude, through the MCP server with the same key.
	s := e.mcpSession(t, token)
	mustOK(t, callTool(t, s, "resume_monitors", map[string]any{"monitor_ids": []string{mon}}), "resume")

	if r := owner.call("DELETE", "/api/v1/monitors/"+mon, nil); r.Status != 204 {
		t.Fatalf("delete: %d", r.Status)
	}
	if r := owner.call("DELETE", "/api/v1/api-keys/"+key["id"].(string), nil); r.Status != 204 {
		t.Fatalf("revoke: %d", r.Status)
	}

	events := logOf(t, owner, "/api/v1/security-log")
	want := []string{"api_key.revoked", "monitor.deleted", "monitor.resumed", "monitor.paused", "api_key.created", "monitor.changed", "monitor.created"}
	if got := strings.Join(actions(events), ","); got != strings.Join(want, ",") {
		t.Fatalf("organization log, newest first:\n got %s\nwant %s", got, strings.Join(want, ","))
	}
	actor := func(action string) map[string]any { return find(t, events, action)["actor"].(map[string]any) }
	if a := actor("monitor.created"); a["type"] != "user" || a["email"] != "owner@example.com" || a["credential_name"] != nil {
		t.Fatalf("created by: %v", a)
	}
	if a := actor("monitor.paused"); a["type"] != "api_key" || a["email"] != "owner@example.com" || a["credential_name"] != "Deploy script" {
		t.Fatalf("paused by: %v", a)
	}
	resumed := find(t, events, "monitor.resumed")
	if a := resumed["actor"].(map[string]any); a["type"] != "api_key" || a["credential_name"] != "Deploy script" || resumed["details"].(map[string]any)["via"] != "mcp" {
		t.Fatalf("resumed through MCP: %v", resumed)
	}
	if resumed["ip"] == nil {
		t.Fatalf("an MCP tool call says where it came from: %v", resumed)
	}
	// The deleted monitor is still named, and an update lists the settings sent, not their values.
	if tg := find(t, events, "monitor.deleted")["target"].(map[string]any); tg["name"] != "Shop front" || tg["id"] != mon {
		t.Fatalf("deleted target: %v", tg)
	}
	if f := fmt.Sprint(find(t, events, "monitor.changed")["details"].(map[string]any)["fields"]); f != "[check_interval_seconds name]" {
		t.Fatalf("changed fields: %s", f)
	}
	if tg := find(t, events, "api_key.revoked")["target"].(map[string]any); tg["name"] != "Deploy script" {
		t.Fatalf("revoked key: %v", tg)
	}

	// Filtering by category, and paging with next_before.
	if got := actions(logOf(t, owner, "/api/v1/security-log?category=api_key")); strings.Join(got, ",") != "api_key.revoked,api_key.created" {
		t.Fatalf("api_key category: %v", got)
	}
	page := owner.call("GET", "/api/v1/security-log?limit=3", nil)
	if n := len(page.Body["events"].([]any)); n != 3 || page.Body["next_before"] == nil {
		t.Fatalf("first page: %d events, next %v", n, page.Body["next_before"])
	}
	rest := logOf(t, owner, fmt.Sprintf("/api/v1/security-log?limit=10&before=%.0f", page.Body["next_before"].(float64)))
	if strings.Join(actions(rest), ",") != strings.Join(want[3:], ",") {
		t.Fatalf("second page: %v", actions(rest))
	}
	for _, q := range []string{"?category=account", "?category=nope", "?limit=0", "?limit=500", "?before=-1", "?before=x"} {
		if r := owner.call("GET", "/api/v1/security-log"+q, nil); r.Status != 400 {
			t.Errorf("%s: %d, want 400", q, r.Status)
		}
	}
	if got := promtest.ToFloat64(m.AuditWrites.WithLabelValues("error")); got != 0 {
		t.Fatalf("%v failed writes", got)
	}
	if got := promtest.ToFloat64(m.AuditWrites.WithLabelValues("ok")); got < 7 {
		t.Fatalf("only %v writes counted", got)
	}
}

func TestOnlyTheOwnerReadsTheOrganizationLogAndNeverWithAKey(t *testing.T) {
	e, owner, org := teamEnv(t)
	member, _ := e.join(t, "member@example.com", e.invite(t, owner, org, "member@example.com"))
	hdr := map[string]string{"X-Organization-ID": org}

	if r := member.callWith(hdr, "GET", "/api/v1/security-log", nil); r.Status != 403 {
		t.Fatalf("a member reads the organization's log: %d %v", r.Status, r.Body)
	}
	e.setOrgPlan(t, org, "agency")
	_, token := newKey(t, owner, map[string]any{"name": "Reader", "scope": "read"})
	if r := e.keyClient(t, token).call("GET", "/api/v1/security-log", nil); r.Status != 403 || errCode(r) != "SESSION_REQUIRED" {
		t.Fatalf("a key reads the log of what keys did: %d %v", r.Status, r.Body)
	}
	if r := e.keyClient(t, token).call("GET", "/api/v1/account/security-log", nil); r.Status != 403 || errCode(r) != "SESSION_REQUIRED" {
		t.Fatalf("a key reads its owner's account log: %d %v", r.Status, r.Body)
	}

	// The team's own story is in the owner's log: the invitation and the member joining, by the member.
	events := logOf(t, owner, "/api/v1/security-log")
	joined := find(t, events, "member.joined")
	if joined["actor"].(map[string]any)["email"] != "member@example.com" {
		t.Fatalf("joined: %v", joined)
	}
	if find(t, events, "member.invited")["target"].(map[string]any)["name"] != "member@example.com" {
		t.Fatalf("invited: %v", find(t, events, "member.invited"))
	}

	// When the member removes their account, the team keeps a note that they left.
	if r := member.call("POST", "/api/v1/account/delete", map[string]string{"password": pw, "confirm_email": "member@example.com"}); r.Status != 204 {
		t.Fatalf("delete account: %d %v", r.Status, r.Body)
	}
	left := find(t, logOf(t, owner, "/api/v1/security-log"), "member.left")
	if left["details"].(map[string]any)["account_deleted"] != true || left["actor"].(map[string]any)["email"] != "member@example.com" {
		t.Fatalf("left: %v", left)
	}
	// Their own log went with their account.
	if n := e.countRows(t, `SELECT count(*) FROM audit_events WHERE account_id IS NOT NULL AND actor_email = 'member@example.com'`); n != 0 {
		t.Fatalf("%d account events outlived the account", n)
	}
}

func TestTheLogCannotBeRewritten(t *testing.T) {
	e := newEnv(t, 1000)
	e.register(t, "alice@example.com")
	if _, err := e.pool.Exec(t.Context(), `UPDATE audit_events SET actor_email = 'someone-else@example.com'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("an event was changed: %v", err)
	}
}
