package httpapi_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/adconversions"
	"hawkeye/internal/billing"
)

func withCookies(t *testing.T, e *env, c *client, cookies ...*http.Cookie) {
	t.Helper()
	u, _ := url.Parse(e.srv.URL)
	c.http.Jar.SetCookies(u, cookies)
}

func TestSignUpAndCheckoutAreRecordedForOpenAIAdsWithTheIDsTheBrowserGets(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{billingCfg: billingCfg, provider: &billing.FakeProvider{Subscriptions: map[string]billing.SubscriptionState{}}, adConv: true})

	c := e.newClient(t)
	withCookies(t, e, c, &http.Cookie{Name: "__oppref", Value: "click.abc"}, &http.Cookie{Name: "__obref", Value: "b.123"})
	r := c.call("POST", "/api/v1/auth/register", map[string]string{"email": "buyer@example.com", "password": pw})
	if r.Status != 201 {
		t.Fatalf("register: %d %v", r.Status, r.Body)
	}
	uid := uuid.MustParse(r.Body["user"].(map[string]any)["id"].(string))
	if r.Body["conversion_event_id"] != adconversions.RegistrationEventID(uid) {
		t.Fatalf("the browser must get the id the server reports with: %v", r.Body["conversion_event_id"])
	}
	var oppref, obref, eventID string
	if err := e.pool.QueryRow(t.Context(), `SELECT oppref, obref, event_id FROM ad_conversion_events WHERE event_type = 'registration_completed' AND user_id = $1`, uid).
		Scan(&oppref, &obref, &eventID); err != nil || oppref != "click.abc" || obref != "b.123" || eventID != r.Body["conversion_event_id"] {
		t.Fatalf("recorded %q %q %q %v", oppref, obref, eventID, err)
	}

	e.verify(t, c, "buyer@example.com")
	co := c.call("POST", "/api/v1/billing/checkout-session", map[string]string{"plan": "pro"})
	if co.Status != 200 {
		t.Fatalf("checkout: %d %v", co.Status, co.Body)
	}
	id, _ := co.Body["conversion_event_id"].(string)
	var n int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM ad_conversion_events WHERE event_type = 'checkout_started' AND event_id = $1 AND user_id = $2 AND oppref = 'click.abc'`,
		id, uid).Scan(&n)
	if id == "" || n != 1 {
		t.Fatalf("checkout id %q recorded %d", id, n)
	}
}

func TestNothingIsRecordedForOpenAIAdsWhenTheVisitorOptedOut(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{adConv: true})
	off := e.newClient(t)
	withCookies(t, e, off, &http.Cookie{Name: "hawkeye_analytics", Value: "off"}, &http.Cookie{Name: "__oppref", Value: "click.abc"})
	if r := off.call("POST", "/api/v1/auth/register", map[string]string{"email": "off@example.com", "password": pw}); r.Status != 201 {
		t.Fatalf("register: %d %v", r.Status, r.Body)
	}
	gpc := e.newClient(t)
	if r := gpc.callWith(map[string]string{"Sec-GPC": "1"}, "POST", "/api/v1/auth/register", map[string]string{"email": "gpc@example.com", "password": pw}); r.Status != 201 {
		t.Fatalf("register: %d %v", r.Status, r.Body)
	}
	// Without OPENAI_ADS_API_KEY nothing is kept either, even for a visitor who allows measurement.
	plain := newEnv(t, 1000)
	plain.register(t, "keyless@example.com")
	var n int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM ad_conversion_events`).Scan(&n)
	var m int
	plain.pool.QueryRow(t.Context(), `SELECT count(*) FROM ad_conversion_events`).Scan(&m)
	if n != 0 || m != 0 {
		t.Fatalf("recorded %d and %d events", n, m)
	}
}
