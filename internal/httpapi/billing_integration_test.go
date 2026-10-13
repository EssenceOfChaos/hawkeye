package httpapi_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/billing"
	"hawkeye/internal/billing/billingtest"
)

var billingCfg = billing.Config{
	SecretKey: "sk_test_x", WebhookSecret: billingtest.Secret, PricePro: "price_pro", PriceAgency: "price_agency", AppBaseURL: "http://app.test",
}

func newBillingEnv(t *testing.T) *env {
	t.Helper()
	return newEnvOpts(t, 1000, envOpts{billingCfg: billingCfg, provider: &billing.FakeProvider{Subscriptions: map[string]billing.SubscriptionState{}}})
}

// webhook posts a raw payload like Stripe would: no cookies, no CSRF header.
func (e *env) webhook(t *testing.T, payload []byte, header string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set("Stripe-Signature", header)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (e *env) signedWebhook(t *testing.T, payload []byte) (int, string) {
	if e.fake != nil {
		billingtest.Mirror(e.fake, payload) // the provider's current state matches the event, the usual case
	}
	return e.webhook(t, payload, billingtest.Sign(payload, billingtest.Secret, time.Now()))
}

func orgIDOf(t *testing.T, c *client) string {
	t.Helper()
	return c.call("GET", "/api/v1/organizations", nil).Body["organizations"].([]any)[0].(map[string]any)["id"].(string)
}

func subView(t *testing.T, c *client) map[string]any {
	t.Helper()
	r := c.call("GET", "/api/v1/billing/subscription", nil)
	if r.Status != 200 {
		t.Fatalf("subscription: %d %v", r.Status, r.Body)
	}
	return r.Body["subscription"].(map[string]any)
}

func TestWebhookEndpointNeedsNoSessionAndVerifiesSignatures(t *testing.T) {
	e := newBillingEnv(t)
	c, _ := e.register(t, "owner@example.com")
	org := orgIDOf(t, c)
	sub := billingtest.Sub{ID: "sub_1", Customer: "cus_1", Status: "active", Price: "price_pro", OrgID: org, PeriodEnd: time.Now().Add(720 * time.Hour).Unix()}
	payload := billingtest.SubscriptionEvent("evt_http_1", "customer.subscription.created", time.Now(), sub)

	// Missing, wrong and tampered signatures are refused and change nothing.
	for name, hdr := range map[string]string{
		"missing":   "",
		"wrong key": billingtest.Sign(payload, "whsec_wrong", time.Now()),
		"stale":     billingtest.Sign(payload, billingtest.Secret, time.Now().Add(-time.Hour)),
	} {
		if st, body := e.webhook(t, payload, hdr); st != 400 || !strings.Contains(body, "INVALID_SIGNATURE") {
			t.Errorf("%s: %d %s", name, st, body)
		}
	}
	if st, _ := e.webhook(t, append(payload, ' '), billingtest.Sign(payload, billingtest.Secret, time.Now())); st != 400 {
		t.Errorf("tampered: %d", st)
	}
	if v := subView(t, c); v["paid_access"] != false {
		t.Fatal("a forged webhook granted access")
	}

	st, body := e.signedWebhook(t, payload)
	if st != 200 || !strings.Contains(body, `"duplicate":false`) {
		t.Fatalf("%d %s", st, body)
	}
	v := subView(t, c)
	if v["paid_access"] != true || v["plan"].(map[string]any)["code"] != "pro" || v["status"] != "active" {
		t.Fatalf("%v", v)
	}
	// Redelivery is acknowledged as a duplicate.
	if st, body := e.signedWebhook(t, payload); st != 200 || !strings.Contains(body, `"duplicate":true`) {
		t.Fatalf("%d %s", st, body)
	}
	var n int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM billing_webhook_events`).Scan(&n)
	if n != 1 {
		t.Fatalf("events stored: %d", n)
	}
}

func TestWebhookEndpointWhenBillingIsNotConfigured(t *testing.T) {
	e := newEnv(t, 1000)
	payload := billingtest.Event("evt_1", "customer.subscription.created", time.Now(), map[string]any{"id": "sub"})
	if st, body := e.signedWebhook(t, payload); st != 503 || !strings.Contains(body, "BILLING_NOT_CONFIGURED") {
		t.Fatalf("%d %s", st, body)
	}
}

func TestWebhookRejectsOversizedAndMalformedBodies(t *testing.T) {
	e := newBillingEnv(t)
	big := bytes.Repeat([]byte("x"), 2<<20)
	if st, _ := e.webhook(t, big, billingtest.Sign(big, billingtest.Secret, time.Now())); st != 413 {
		t.Fatalf("oversized: %d", st)
	}
	junk := []byte("this is not json")
	if st, _ := e.webhook(t, junk, billingtest.Sign(junk, billingtest.Secret, time.Now())); st != 400 {
		t.Fatalf("signed garbage: %d", st)
	}
}

func TestPlansEndpointReportsWhetherBillingIsAvailable(t *testing.T) {
	on, off := newBillingEnv(t), newEnv(t, 1000)
	for env, want := range map[*env]bool{on: true, off: false} {
		r := env.newClient(t).call("GET", "/api/v1/plans", nil)
		if r.Body["billing_enabled"] != want {
			t.Errorf("billing_enabled = %v, want %v", r.Body["billing_enabled"], want)
		}
	}
}

func TestBillingEndpointsRequireOwnerAndVerifiedEmail(t *testing.T) {
	e := newBillingEnv(t)
	anon := e.newClient(t)
	for _, p := range []string{"/api/v1/billing/checkout-session", "/api/v1/billing/portal-session", "/api/v1/billing/cancel-subscription", "/api/v1/billing/sync"} {
		if r := anon.call("POST", p, map[string]string{"plan": "pro"}); r.Status != 401 {
			t.Errorf("anonymous %s: %d", p, r.Status)
		}
	}
	if r := anon.call("GET", "/api/v1/billing/subscription", nil); r.Status != 401 {
		t.Errorf("anonymous subscription: %d", r.Status)
	}

	c, _ := e.register(t, "owner@example.com") // not verified yet
	for _, p := range []string{"/api/v1/billing/checkout-session", "/api/v1/billing/portal-session", "/api/v1/billing/cancel-subscription"} {
		if r := c.call("POST", p, map[string]string{"plan": "pro"}); r.Status != 403 || errCode(r) != "EMAIL_NOT_VERIFIED" {
			t.Errorf("unverified %s: %d %v", p, r.Status, r.Body)
		}
	}
	if len(e.fake.Checkouts) != 0 {
		t.Fatal("no provider call may happen before the email is verified")
	}

	// A plain member of someone else's organization cannot spend their money.
	owner, org := e.register(t, "boss@example.com")
	e.verify(t, owner, "boss@example.com")
	member, _ := e.register(t, "member@example.com")
	e.verify(t, member, "member@example.com")
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email='member@example.com'`, org)
	for _, p := range []string{"/api/v1/billing/checkout-session", "/api/v1/billing/portal-session", "/api/v1/billing/cancel-subscription", "/api/v1/billing/sync"} {
		if st := member.doWithHeaderBody("POST", p, org, `{"plan":"pro"}`); st != 403 {
			t.Errorf("member %s: %d", p, st)
		}
	}
	if st := member.doWithHeader("GET", "/api/v1/billing/subscription", org); st != 200 {
		t.Errorf("members may view the plan: %d", st)
	}
	if len(e.fake.Checkouts) != 0 {
		t.Fatal("a member started a checkout")
	}
}

func TestCheckoutSessionUsesServerSidePrices(t *testing.T) {
	e := newBillingEnv(t)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")
	org := orgIDOf(t, c)

	// Client-supplied price/amount fields are ignored: only the plan name is read, and the price comes from configuration.
	r := c.call("POST", "/api/v1/billing/checkout-session", map[string]any{"plan": "pro", "price": "price_evil", "amount": 1, "organization_id": "other"})
	if r.Status != 200 || !strings.HasPrefix(r.Body["url"].(string), "https://checkout.example.test/") {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	ck := e.fake.Checkouts[0]
	if ck.PriceID != "price_pro" || ck.OrganizationID.String() != org || ck.CustomerEmail != "owner@example.com" {
		t.Fatalf("%+v", ck)
	}
	if v := subView(t, c); v["paid_access"] != false {
		t.Fatal("creating a checkout session granted access")
	}

	for _, plan := range []string{"", "free", "platinum", "price_pro"} {
		if r := c.call("POST", "/api/v1/billing/checkout-session", map[string]string{"plan": plan}); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
			t.Errorf("plan %q: %d %v", plan, r.Status, r.Body)
		}
	}
	e.fake.Err = io.ErrUnexpectedEOF
	if r := c.call("POST", "/api/v1/billing/checkout-session", map[string]string{"plan": "agency"}); r.Status != 502 || errCode(r) != "BILLING_PROVIDER_ERROR" ||
		strings.Contains(r.Body["error"].(map[string]any)["message"].(string), "EOF") {
		t.Fatalf("provider failure must be generic: %d %v", r.Status, r.Body)
	}
}

func TestCheckoutWhenBillingIsDisabled(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")
	if r := c.call("POST", "/api/v1/billing/checkout-session", map[string]string{"plan": "pro"}); r.Status != 503 || errCode(r) != "BILLING_NOT_CONFIGURED" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if v := subView(t, c); v["billing_enabled"] != false || v["plan"].(map[string]any)["code"] != "free" {
		t.Fatalf("%v", v)
	}
}

// Phase 7 acceptance, end to end: upgrade via verified webhooks, limits change, downgrade keeps data.
func TestUpgradeAndDowngradeFlow(t *testing.T) {
	e := newBillingEnv(t)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")
	org := orgIDOf(t, c)
	now := time.Now()

	for i := 0; i < 5; i++ {
		if r := c.call("POST", "/api/v1/monitors", monitorBody("m")); r.Status != 201 {
			t.Fatalf("free monitor %d: %d", i, r.Status)
		}
	}
	if r := c.call("POST", "/api/v1/monitors", monitorBody("sixth")); r.Status != 403 {
		t.Fatalf("free limit: %d", r.Status)
	}

	// 1. Checkout, then the browser returns to ?checkout=success: nothing has been granted yet.
	c.call("POST", "/api/v1/billing/checkout-session", map[string]string{"plan": "pro"})
	if r := c.call("POST", "/api/v1/monitors", monitorBody("sixth")); r.Status != 403 {
		t.Fatalf("the redirect/session alone must not raise limits: %d", r.Status)
	}

	// 2. A payment that is not completed (incomplete) grants nothing either.
	sub := billingtest.Sub{ID: "sub_1", Customer: "cus_1", Status: "incomplete", Price: "price_pro", OrgID: org, PeriodEnd: now.Add(720 * time.Hour).Unix()}
	e.signedWebhook(t, billingtest.CheckoutCompleted("evt_1", now, org, "cus_1", "sub_1"))
	e.signedWebhook(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.created", now.Add(time.Second), sub))
	if r := c.call("POST", "/api/v1/monitors", monitorBody("sixth")); r.Status != 403 {
		t.Fatalf("incomplete payment: %d", r.Status)
	}

	// 3. The payment completes: the verified event raises the limits.
	sub.Status = "active"
	e.signedWebhook(t, billingtest.SubscriptionEvent("evt_3", "customer.subscription.updated", now.Add(2*time.Second), sub))
	u := c.call("GET", "/api/v1/usage", nil).Body["usage"].(map[string]any)
	if u["monitor_limit"].(float64) != 50 || u["plan"].(map[string]any)["code"] != "pro" {
		t.Fatalf("%v", u)
	}
	for i := 0; i < 3; i++ {
		if r := c.call("POST", "/api/v1/monitors", monitorBody("paid")); r.Status != 201 {
			t.Fatalf("paid monitor %d: %d %v", i, r.Status, r.Body)
		}
	}
	fast := monitorBody("fast")
	fast["check_interval_seconds"] = 60
	if r := c.call("POST", "/api/v1/monitors", fast); r.Status != 201 {
		t.Fatalf("pro allows 1-minute checks: %d %v", r.Status, r.Body)
	}

	// 4. A failed renewal: access continues while Stripe retries (past_due)…
	sub.Status = "past_due"
	e.signedWebhook(t, billingtest.SubscriptionEvent("evt_4", "customer.subscription.updated", now.Add(3*time.Second), sub))
	if v := subView(t, c); v["paid_access"] != true || v["status"] != "past_due" {
		t.Fatalf("%v", v)
	}
	// …and is revoked when the subscription ends.
	sub.Status = "canceled"
	e.signedWebhook(t, billingtest.SubscriptionEvent("evt_5", "customer.subscription.deleted", now.Add(4*time.Second), sub))
	v := subView(t, c)
	if v["paid_access"] != false || v["plan"].(map[string]any)["code"] != "free" {
		t.Fatalf("%v", v)
	}

	// 5. Downgrade: all 9 monitors survive, creation is blocked and the explanation says how to recover.
	list := c.call("GET", "/api/v1/monitors", nil)
	if list.Body["total"].(float64) != 9 {
		t.Fatalf("monitors must survive the downgrade: %v", list.Body["total"])
	}
	r := c.call("POST", "/api/v1/monitors", monitorBody("blocked"))
	msg := ""
	if r.Status == 403 {
		msg = r.Body["error"].(map[string]any)["message"].(string)
	}
	if r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" || !strings.Contains(msg, "delete 4") {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if u := c.call("GET", "/api/v1/usage", nil).Body["usage"].(map[string]any); u["over_limit"] != true {
		t.Fatalf("%v", u)
	}
}

func TestPortalCancelAndSyncEndpoints(t *testing.T) {
	e := newBillingEnv(t)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")
	org := orgIDOf(t, c)

	if r := c.call("POST", "/api/v1/billing/portal-session", nil); r.Status != 409 || errCode(r) != "NO_BILLING_ACCOUNT" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/billing/cancel-subscription", nil); r.Status != 409 || errCode(r) != "NO_ACTIVE_SUBSCRIPTION" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}

	sub := billingtest.Sub{ID: "sub_1", Customer: "cus_1", Status: "active", Price: "price_pro", OrgID: org, PeriodEnd: time.Now().Add(720 * time.Hour).Unix()}
	e.signedWebhook(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", time.Now(), sub))
	st, _ := billing.ParseSubscription(mustJSONBytes(sub.Object()))
	e.fake.Subscriptions["sub_1"] = st

	if r := c.call("POST", "/api/v1/billing/portal-session", nil); r.Status != 200 || !strings.Contains(r.Body["url"].(string), "cus_1") {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/billing/checkout-session", map[string]string{"plan": "agency"}); r.Status != 409 || errCode(r) != "ALREADY_SUBSCRIBED" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	r := c.call("POST", "/api/v1/billing/cancel-subscription", nil)
	v := r.Body["subscription"].(map[string]any)
	if r.Status != 200 || v["cancel_at_period_end"] != true || v["paid_access"] != true {
		t.Fatalf("cancellation keeps access until the period ends: %d %v", r.Status, r.Body)
	}

	// Sync repairs a missed cancellation webhook.
	gone := sub
	gone.Status = "canceled"
	gs, _ := billing.ParseSubscription(mustJSONBytes(gone.Object()))
	e.fake.Subscriptions["sub_1"] = gs
	r = c.call("POST", "/api/v1/billing/sync", nil)
	if r.Status != 200 || r.Body["subscription"].(map[string]any)["paid_access"] != false {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestBillingIsTenantScoped(t *testing.T) {
	e := newBillingEnv(t)
	alice, aliceOrg := e.register(t, "alice@example.com")
	bob, _ := e.register(t, "bob@example.com")
	e.verify(t, bob, "bob@example.com")
	e.signedWebhook(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", time.Now(),
		billingtest.Sub{ID: "sub_a", Customer: "cus_a", Status: "active", Price: "price_pro", OrgID: aliceOrg}))
	if v := subView(t, alice); v["paid_access"] != true {
		t.Fatal("alice should be on pro")
	}
	if v := subView(t, bob); v["paid_access"] != false || v["can_manage"] != false {
		t.Fatalf("bob sees alice's billing: %v", v)
	}
	if st := bob.doWithHeader("GET", "/api/v1/billing/subscription", aliceOrg); st != 404 {
		t.Fatalf("foreign org header: %d", st)
	}
	if st := bob.doWithHeaderBody("POST", "/api/v1/billing/portal-session", aliceOrg, ""); st != 404 {
		t.Fatalf("foreign org portal: %d", st)
	}
}
