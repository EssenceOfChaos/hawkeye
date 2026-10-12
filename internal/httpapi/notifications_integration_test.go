package httpapi_test

import (
	"fmt"
	"testing"
	"time"
)

func (e *env) verify(t *testing.T, c *client, addr string) {
	t.Helper()
	tok := e.lastToken(t, addr, "Verify")
	if r := c.call("POST", "/api/v1/auth/verify-email", map[string]string{"token": tok}); r.Status != 204 {
		t.Fatalf("verify %s: %d", addr, r.Status)
	}
}

func settings(t *testing.T, c *client) []map[string]any {
	t.Helper()
	r := c.call("GET", "/api/v1/notification-settings", nil)
	if r.Status != 200 {
		t.Fatalf("list settings: %d %v", r.Status, r.Body)
	}
	var out []map[string]any
	for _, s := range r.Body["notification_settings"].([]any) {
		out = append(out, s.(map[string]any))
	}
	return out
}

func TestOwnerAddressIsTheDefaultRecipientAndActivatesOnVerification(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")

	s := settings(t, c)
	if len(s) != 1 || s[0]["destination"] != "owner@example.com" || s[0]["verified"] != false || s[0]["enabled"] != true {
		t.Fatalf("default setting: %v", s)
	}
	e.verify(t, c, "owner@example.com")
	if s := settings(t, c); s[0]["verified"] != true {
		t.Fatalf("verifying the account email must activate the default recipient: %v", s)
	}
}

func TestAddingRecipientRequiresVerifiedEmail(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "client@example.com"})
	if r.Status != 403 || errCode(r) != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if len(e.mail.Messages()) != 1 { // only the registration verification email
		t.Fatalf("no mail may be sent to third parties before the account is verified: %d messages", len(e.mail.Messages()))
	}
}

func TestExternalRecipientMustConfirmBeforeReceivingAlerts(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")

	r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": " Client@Example.com "})
	if r.Status != 201 {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	s := r.Body["notification_setting"].(map[string]any)
	if s["destination"] != "client@example.com" || s["verified"] != false || s["user_id"] != nil {
		t.Fatalf("%v", s)
	}
	tok := e.lastToken(t, "client@example.com", "Confirm")

	// The recipient clicks the link without being logged in.
	anon := e.newClient(t)
	if r := anon.call("POST", "/api/v1/notification-settings/confirm", map[string]string{"token": "garbage"}); r.Status != 400 || errCode(r) != "INVALID_TOKEN" {
		t.Fatalf("bad token: %d %v", r.Status, r.Body)
	}
	if r := anon.call("POST", "/api/v1/notification-settings/confirm", map[string]string{"token": tok}); r.Status != 204 {
		t.Fatalf("confirm: %d", r.Status)
	}
	if r := anon.call("POST", "/api/v1/notification-settings/confirm", map[string]string{"token": tok}); r.Status != 400 {
		t.Fatalf("reuse: %d", r.Status)
	}
	var verified bool
	for _, x := range settings(t, c) {
		if x["destination"] == "client@example.com" {
			verified = x["verified"].(bool)
		}
	}
	if !verified {
		t.Fatal("confirmed address should be verified")
	}
}

func TestOwnAddressIsVerifiedImmediately(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")
	c.call("DELETE", "/api/v1/notification-settings/"+settings(t, c)[0]["id"].(string), nil)

	before := len(e.mail.Messages())
	r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "owner@example.com"})
	if r.Status != 201 || r.Body["notification_setting"].(map[string]any)["verified"] != true || len(e.mail.Messages()) != before {
		t.Fatalf("%d %v mails=%d", r.Status, r.Body, len(e.mail.Messages())-before)
	}
}

func TestResendConfirmationInvalidatesOldLink(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")
	id := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "client@example.com"}).Body["notification_setting"].(map[string]any)["id"].(string)
	old := e.lastToken(t, "client@example.com", "Confirm")

	if r := c.call("POST", "/api/v1/notification-settings/"+id+"/resend-confirmation", nil); r.Status != 202 {
		t.Fatalf("resend: %d", r.Status)
	}
	if r := e.newClient(t).call("POST", "/api/v1/notification-settings/confirm", map[string]string{"token": old}); r.Status != 400 {
		t.Fatalf("old link: %d", r.Status)
	}
	if r := e.newClient(t).call("POST", "/api/v1/notification-settings/confirm", map[string]string{"token": e.lastToken(t, "client@example.com", "Confirm")}); r.Status != 204 {
		t.Fatalf("new link: %d", r.Status)
	}
}

func TestSettingsCRUDValidationAndLimits(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")

	if r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "not-an-email"}); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "a@example.com", "channel": "sms"}); r.Status != 400 {
		t.Fatalf("sms: %d", r.Status)
	}
	id := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "a@example.com"}).Body["notification_setting"].(map[string]any)["id"].(string)
	if r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "A@example.com"}); r.Status != 409 || errCode(r) != "DESTINATION_EXISTS" {
		t.Fatalf("duplicate: %d %v", r.Status, r.Body)
	}

	r := c.call("PATCH", "/api/v1/notification-settings/"+id, map[string]any{"enabled": false})
	if r.Status != 200 || r.Body["notification_setting"].(map[string]any)["enabled"] != false {
		t.Fatalf("patch: %d %v", r.Status, r.Body)
	}
	if r := c.call("PATCH", "/api/v1/notification-settings/"+id, map[string]any{"destination": "x@example.com"}); r.Status != 400 {
		t.Fatalf("destination must not be editable: %d", r.Status)
	}

	// Fill up to the cap (the default recipient and "a@" already exist).
	for i := 0; i < 8; i++ {
		if r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": fmt.Sprintf("u%d@example.com", i)}); r.Status != 201 {
			t.Fatalf("create %d: %d %v", i, r.Status, r.Body)
		}
	}
	if r := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "overflow@example.com"}); r.Status != 403 || errCode(r) != "NOTIFICATION_SETTINGS_LIMIT" {
		t.Fatalf("cap: %d %v", r.Status, r.Body)
	}

	if r := c.call("DELETE", "/api/v1/notification-settings/"+id, nil); r.Status != 204 {
		t.Fatalf("delete: %d", r.Status)
	}
	if r := c.call("DELETE", "/api/v1/notification-settings/"+id, nil); r.Status != 404 {
		t.Fatalf("delete twice: %d", r.Status)
	}
}

func TestSettingsTenantIsolationAndRoles(t *testing.T) {
	e := newEnv(t, 1000)
	alice, aliceOrg := e.register(t, "alice@example.com")
	bob, _ := e.register(t, "bob@example.com")
	e.verify(t, alice, "alice@example.com")
	e.verify(t, bob, "bob@example.com")
	aliceSetting := settings(t, alice)[0]["id"].(string)

	if r := bob.call("PATCH", "/api/v1/notification-settings/"+aliceSetting, map[string]any{"enabled": false}); r.Status != 404 {
		t.Fatalf("cross-tenant patch: %d", r.Status)
	}
	if r := bob.call("DELETE", "/api/v1/notification-settings/"+aliceSetting, nil); r.Status != 404 {
		t.Fatalf("cross-tenant delete: %d", r.Status)
	}
	for _, s := range settings(t, bob) {
		if s["id"] == aliceSetting {
			t.Fatal("bob can see alice's settings")
		}
	}
	if st := bob.doWithHeader("GET", "/api/v1/notification-settings", aliceOrg); st != 404 {
		t.Fatalf("foreign org header: %d", st)
	}
	if s := settings(t, alice); s[0]["enabled"] != true {
		t.Fatal("alice's setting was changed")
	}

	// A plain member of Alice's organization can view but not change recipients.
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role)
		SELECT $1, id, 'member' FROM users WHERE email='bob@example.com'`, aliceOrg)
	hdr := func(method, path string, body string) int { return bob.doWithHeaderBody(method, path, aliceOrg, body) }
	if hdr("GET", "/api/v1/notification-settings", "") != 200 {
		t.Fatal("members may view recipients")
	}
	for name, st := range map[string]int{
		"create": hdr("POST", "/api/v1/notification-settings", `{"destination":"evil@example.com"}`),
		"patch":  hdr("PATCH", "/api/v1/notification-settings/"+aliceSetting, `{"enabled":false}`),
		"delete": hdr("DELETE", "/api/v1/notification-settings/"+aliceSetting, ""),
	} {
		if st != 403 {
			t.Errorf("member %s: want 403, got %d", name, st)
		}
	}
}

func seedIncident(t *testing.T, e *env, monitorID string, startedAgo time.Duration, open bool) {
	t.Helper()
	q := `INSERT INTO incidents (monitor_id, started_at, resolved_at, status, cause_category, summary)
		VALUES ($1, now() - $2::interval, CASE WHEN $3 THEN NULL ELSE now() - $2::interval + interval '10 minutes' END,
			CASE WHEN $3 THEN 'open' ELSE 'resolved' END, 'timeout', 'Request timed out')`
	if _, err := e.pool.Exec(t.Context(), q, monitorID, startedAgo, open); err != nil {
		t.Fatal(err)
	}
}

func TestIncidentEndpoints(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	m1 := c.call("POST", "/api/v1/monitors", monitorBody("One")).Body["monitor"].(map[string]any)["id"].(string)
	m2 := c.call("POST", "/api/v1/monitors", monitorBody("Two")).Body["monitor"].(map[string]any)["id"].(string)
	seedIncident(t, e, m1, 48*time.Hour, false)
	seedIncident(t, e, m1, time.Hour, true)
	seedIncident(t, e, m2, 24*time.Hour, false)

	r := c.call("GET", "/api/v1/incidents", nil)
	list := r.Body["incidents"].([]any)
	if r.Status != 200 || len(list) != 3 || r.Body["total"].(float64) != 3 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	first := list[0].(map[string]any)
	if first["status"] != "open" || first["monitor_name"] != "One" || first["resolved_at"] != nil || first["duration_seconds"].(float64) < 3500 {
		t.Fatalf("newest incident: %v", first)
	}

	if r := c.call("GET", "/api/v1/incidents?status=resolved", nil); len(r.Body["incidents"].([]any)) != 2 {
		t.Fatalf("status filter: %v", r.Body)
	}
	if r := c.call("GET", "/api/v1/monitors/"+m1+"/incidents", nil); r.Status != 200 || len(r.Body["incidents"].([]any)) != 2 {
		t.Fatalf("monitor incidents: %d %v", r.Status, r.Body)
	}
	from := time.Now().Add(-30 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	if r := c.call("GET", "/api/v1/incidents?from="+from+"&to="+to, nil); len(r.Body["incidents"].([]any)) != 1 {
		t.Fatalf("time range: %v", r.Body)
	}
	if r := c.call("GET", "/api/v1/incidents?limit=1&offset=1", nil); len(r.Body["incidents"].([]any)) != 1 || r.Body["total"].(float64) != 3 {
		t.Fatalf("pagination: %v", r.Body)
	}
	for _, q := range []string{"status=bogus", "from=yesterday", "limit=0", "offset=-1"} {
		if r := c.call("GET", "/api/v1/incidents?"+q, nil); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %v", q, r.Status, r.Body)
		}
	}
}

func TestIncidentTenantIsolation(t *testing.T) {
	e := newEnv(t, 1000)
	alice, _ := e.register(t, "alice@example.com")
	bob, _ := e.register(t, "bob@example.com")
	m := alice.call("POST", "/api/v1/monitors", monitorBody("Secret")).Body["monitor"].(map[string]any)["id"].(string)
	seedIncident(t, e, m, time.Hour, true)

	if r := bob.call("GET", "/api/v1/monitors/"+m+"/incidents", nil); r.Status != 404 {
		t.Fatalf("bob reading alice's monitor incidents: %d", r.Status)
	}
	if r := bob.call("GET", "/api/v1/incidents", nil); r.Body["total"].(float64) != 0 {
		t.Fatalf("incident list leaks: %v", r.Body)
	}
	if r := alice.call("GET", "/api/v1/incidents", nil); r.Body["total"].(float64) != 1 {
		t.Fatalf("alice: %v", r.Body)
	}
}
