package httpapi_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/billing/billingtest"
)

// ---- change password

func TestChangePasswordRequiresCurrentPasswordAndSignsOutOtherDevices(t *testing.T) {
	e := newEnv(t, 1000)
	laptop, _ := e.register(t, "dev@example.com")
	phone := e.newClient(t)
	if r := phone.call("POST", "/api/v1/auth/login", map[string]string{"email": "dev@example.com", "password": pw}); r.Status != 200 {
		t.Fatalf("second login: %d", r.Status)
	}

	for name, c := range map[string]struct {
		body map[string]string
		want int
		code string
	}{
		"wrong current":  {map[string]string{"current_password": "not-my-password", "new_password": "a-brand-new-password"}, 403, "WRONG_PASSWORD"},
		"weak new":       {map[string]string{"current_password": pw, "new_password": "short"}, 400, "VALIDATION_ERROR"},
		"unchanged":      {map[string]string{"current_password": pw, "new_password": pw}, 400, "VALIDATION_ERROR"},
		"missing fields": {map[string]string{}, 400, "VALIDATION_ERROR"},
	} {
		if r := laptop.call("POST", "/api/v1/auth/change-password", c.body); r.Status != c.want || errCode(r) != c.code {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}
	if r := phone.call("GET", "/api/v1/auth/me", nil); r.Status != 200 {
		t.Fatal("failed attempts must not sign anyone out")
	}

	if r := laptop.call("POST", "/api/v1/auth/change-password", map[string]string{"current_password": pw, "new_password": "a-brand-new-password"}); r.Status != 204 {
		t.Fatalf("change: %d %v", r.Status, r.Body)
	}
	if r := laptop.call("GET", "/api/v1/auth/me", nil); r.Status != 200 {
		t.Fatalf("the device that changed the password stays signed in: %d", r.Status)
	}
	if r := phone.call("GET", "/api/v1/auth/me", nil); r.Status != 401 {
		t.Fatalf("other devices must be signed out: %d", r.Status)
	}
	fresh := e.newClient(t)
	if r := fresh.call("POST", "/api/v1/auth/login", map[string]string{"email": "dev@example.com", "password": pw}); r.Status != 401 {
		t.Fatalf("old password still works: %d", r.Status)
	}
	if r := fresh.call("POST", "/api/v1/auth/login", map[string]string{"email": "dev@example.com", "password": "a-brand-new-password"}); r.Status != 200 {
		t.Fatalf("new password: %d", r.Status)
	}
	var notified bool
	for _, m := range e.mail.Messages() {
		if m.To == "dev@example.com" && strings.Contains(m.Subject, "password was changed") {
			notified = true
		}
	}
	if !notified {
		t.Fatal("the account owner must be emailed about a password change")
	}
	if r := e.newClient(t).call("POST", "/api/v1/auth/change-password", map[string]string{"current_password": "x", "new_password": "y"}); r.Status != 401 {
		t.Fatalf("anonymous: %d", r.Status)
	}
}

// ---- export

func TestExportContainsOnlyTheCallersDataAndNoSecrets(t *testing.T) {
	e := newBillingEnv(t)
	alice, aliceOrg := e.register(t, "alice@example.com")
	e.verify(t, alice, "alice@example.com")
	bob, _ := e.register(t, "bob@example.com")
	m := newMonitorID(t, alice, "Alice Site")
	newMonitorID(t, bob, "Bob's Secret Site")
	seedIncident(t, e, m, time.Hour, false)
	seedResults(t, e, m, 12, time.Minute, time.Minute, 0, 100)
	alice.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "client@example.net"})
	e.signedWebhook(t, billingtest.SubscriptionEvent("evt_x", "customer.subscription.created", time.Now(),
		billingtest.Sub{ID: "sub_secretid", Customer: "cus_secretid", Status: "active", Price: "price_pro", OrgID: aliceOrg, PeriodEnd: time.Now().Add(720 * time.Hour).Unix()}))

	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/account/export", nil)
	for _, ck := range alice.http.Jar.Cookies(req.URL) {
		req.AddCookie(ck)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	body := string(raw)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("%d %v", res.StatusCode, res.Header)
	}
	for _, want := range []string{"alice@example.com", "Alice Site", "client@example.net", `"plan": "pro"`, `"check_results_retained_count": 12`, `"role": "owner"`} {
		if !strings.Contains(body, want) {
			t.Errorf("export missing %q", want)
		}
	}
	for _, leaked := range []string{"bob@example.com", "Bob's Secret Site", "cus_secretid", "sub_secretid", "$argon2id"} {
		if strings.Contains(body, leaked) {
			t.Errorf("export leaked %q", leaked)
		}
	}
	// No field of the export may even be NAMED like a credential.
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch x := v.(type) {
		case map[string]any:
			for k, vv := range x {
				lk := strings.ToLower(k)
				for _, bad := range []string{"password", "token", "secret", "session", "hash", "customer_id", "subscription_id"} {
					if strings.Contains(lk, bad) {
						t.Errorf("export has a credential-like field %s.%s", path, k)
					}
				}
				walk(vv, path+"."+k)
			}
		case []any:
			for _, vv := range x {
				walk(vv, path+"[]")
			}
		}
	}
	walk(parsed, "$")
	if r := e.newClient(t).call("GET", "/api/v1/account/export", nil); r.Status != 401 {
		t.Fatalf("anonymous export: %d", r.Status)
	}
}

// ---- deletion

func TestDeleteAccountNeedsPasswordAndTypedEmail(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "keep@example.com")
	for name, body := range map[string]map[string]string{
		"wrong password": {"password": "wrong-password-123", "confirm_email": "keep@example.com"},
		"wrong email":    {"password": pw, "confirm_email": "someone-else@example.com"},
		"missing":        {},
	} {
		r := c.call("POST", "/api/v1/account/delete", body)
		if r.Status != 403 && r.Status != 400 {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}
	if r := c.call("GET", "/api/v1/auth/me", nil); r.Status != 200 {
		t.Fatal("a rejected deletion must change nothing")
	}
	if e.countRows(t, `SELECT count(*) FROM users`) != 1 {
		t.Fatal("user was deleted")
	}
	if r := e.newClient(t).call("POST", "/api/v1/account/delete", map[string]string{"password": pw, "confirm_email": "keep@example.com"}); r.Status != 401 {
		t.Fatalf("anonymous: %d", r.Status)
	}
}

func (e *env) countRows(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteAccountRemovesEverythingOfTheUserAndNothingElse(t *testing.T) {
	e := newEnv(t, 1000)
	alice, _ := e.register(t, "alice@example.com")
	bob, bobOrg := e.register(t, "bob@example.com")
	e.verify(t, alice, "alice@example.com")
	m := newMonitorID(t, alice, "Alice Site")
	bm := newMonitorID(t, bob, "Bob Site")
	seedResults(t, e, m, 20, time.Minute, time.Minute, 0, 100)
	seedResults(t, e, bm, 20, time.Minute, time.Minute, 0, 100)
	seedIncident(t, e, m, time.Hour, true)
	alice.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "someone@example.net"})
	// Alice is also a plain member of Bob's organization: that membership must just disappear.
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email='alice@example.com'`, bobOrg)

	r := alice.call("POST", "/api/v1/account/delete", map[string]string{"password": pw, "confirm_email": " ALICE@example.com "})
	if r.Status != 204 {
		t.Fatalf("delete: %d %v", r.Status, r.Body)
	}
	if st := alice.call("GET", "/api/v1/auth/me", nil).Status; st != 401 {
		t.Fatalf("session must be gone: %d", st)
	}
	if r := e.newClient(t).call("POST", "/api/v1/auth/login", map[string]string{"email": "alice@example.com", "password": pw}); r.Status != 401 {
		t.Fatalf("deleted account can still log in: %d", r.Status)
	}
	gone := map[string]int{
		"users":                 e.countRows(t, `SELECT count(*) FROM users WHERE email='alice@example.com'`),
		"sessions of alice":     e.countRows(t, `SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.email='alice@example.com'`),
		"alice's monitor":       e.countRows(t, `SELECT count(*) FROM monitors WHERE id=$1`, m),
		"alice's results":       e.countRows(t, `SELECT count(*) FROM check_results WHERE monitor_id=$1`, m),
		"alice's incidents":     e.countRows(t, `SELECT count(*) FROM incidents WHERE monitor_id=$1`, m),
		"alice's recipients":    e.countRows(t, `SELECT count(*) FROM notification_settings WHERE destination IN ('alice@example.com','someone@example.net')`),
		"alice's organizations": e.countRows(t, `SELECT count(*) FROM organizations WHERE name LIKE 'alice%'`),
		"alice's subscriptions": e.countRows(t, `SELECT count(*) FROM subscriptions s LEFT JOIN organizations o ON o.id=s.organization_id WHERE o.id IS NULL`),
		"alice's tokens":        e.countRows(t, `SELECT count(*) FROM email_verification_tokens`) - e.countRows(t, `SELECT count(*) FROM email_verification_tokens t JOIN users u ON u.id=t.user_id WHERE u.email='bob@example.com'`),
		"alice in bob's org":    e.countRows(t, `SELECT count(*) FROM organization_members WHERE organization_id=$1 AND role='member'`, bobOrg),
	}
	for what, n := range gone {
		if n != 0 {
			t.Errorf("%s: %d rows remain", what, n)
		}
	}
	// Bob is untouched.
	if e.countRows(t, `SELECT count(*) FROM monitors WHERE id=$1`, bm) != 1 || e.countRows(t, `SELECT count(*) FROM check_results WHERE monitor_id=$1`, bm) != 20 ||
		e.countRows(t, `SELECT count(*) FROM organizations WHERE id=$1`, bobOrg) != 1 {
		t.Fatal("another tenant's data was affected")
	}
	if r := bob.call("GET", "/api/v1/monitors", nil); r.Status != 200 || r.Body["total"].(float64) != 1 {
		t.Fatalf("bob: %d %v", r.Status, r.Body)
	}
	var farewell bool
	for _, m := range e.mail.Messages() {
		if m.To == "alice@example.com" && strings.Contains(m.Subject, "deleted") {
			farewell = true
		}
	}
	if !farewell {
		t.Fatal("a confirmation email should be sent")
	}
}

func TestCannotDeleteAnAccountThatOwnsASharedOrganization(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.register(t, "colleague@example.com")
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email='colleague@example.com'`, org)
	r := owner.call("POST", "/api/v1/account/delete", map[string]string{"password": pw, "confirm_email": "owner@example.com"})
	if r.Status != 409 || errCode(r) != "OWNS_SHARED_ORGANIZATION" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if e.countRows(t, `SELECT count(*) FROM users`) != 2 || e.countRows(t, `SELECT count(*) FROM organizations WHERE id=$1`, org) != 1 {
		t.Fatal("a refused deletion must delete nothing (the colleague's data would have gone with it)")
	}
}

func TestDeletingAPayingAccountDeletesTheStripeCustomerFirst(t *testing.T) {
	e := newBillingEnv(t)
	c, org := e.register(t, "payer@example.com")
	e.signedWebhook(t, billingtest.SubscriptionEvent("evt_pay", "customer.subscription.created", time.Now(),
		billingtest.Sub{ID: "sub_1", Customer: "cus_payer", Status: "active", Price: "price_pro", OrgID: org}))
	if v := subView(t, c); v["paid_access"] != true {
		t.Fatal("setup")
	}
	if e.countRows(t, `SELECT count(*) FROM billing_webhook_events`) == 0 {
		t.Fatal("setup: expected a stored event")
	}

	// Stripe is down: nothing is deleted, so a paying customer can never be left billed without an account.
	e.fake.Err = errors.New("stripe: 500")
	r := c.call("POST", "/api/v1/account/delete", map[string]string{"password": pw, "confirm_email": "payer@example.com"})
	if r.Status != 502 || errCode(r) != "BILLING_PROVIDER_ERROR" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if e.countRows(t, `SELECT count(*) FROM users`) != 1 {
		t.Fatal("the account was deleted although the subscription could not be cancelled")
	}

	e.fake.Err = nil
	if r := c.call("POST", "/api/v1/account/delete", map[string]string{"password": pw, "confirm_email": "payer@example.com"}); r.Status != 204 {
		t.Fatalf("retry: %d %v", r.Status, r.Body)
	}
	if len(e.fake.CustomersDeleted) != 1 || e.fake.CustomersDeleted[0] != "cus_payer" {
		t.Fatalf("customers deleted at the provider: %v", e.fake.CustomersDeleted)
	}
	if e.countRows(t, `SELECT count(*) FROM users`) != 0 || e.countRows(t, `SELECT count(*) FROM subscriptions`) != 0 {
		t.Fatal("local data should be gone")
	}
	if e.countRows(t, `SELECT count(*) FROM billing_webhook_events WHERE payload::text LIKE '%cus_payer%'`) != 0 {
		t.Fatal("stored webhook payloads mentioning the customer must be removed")
	}
}
