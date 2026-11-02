package httpapi_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A mail client's "Unsubscribe" button POSTs "List-Unsubscribe=One-Click" to the URL in the header: no cookies, no CSRF token, no JSON.
func TestOneClickUnsubscribeNeedsNoSessionOrCSRFToken(t *testing.T) {
	e := newEnv(t, 1000)
	e.register(t, "reader@example.org")
	var token string
	e.pool.QueryRow(context.Background(), `SELECT unsubscribe_token::text FROM users WHERE email = 'reader@example.org'`).Scan(&token)

	post := func(tok string) *http.Response {
		res, err := http.Post(e.srv.URL+"/api/v1/lifecycle/unsubscribe?token="+url.QueryEscape(tok), "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click"))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := post(token); res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var at *time.Time
	e.pool.QueryRow(context.Background(), `SELECT lifecycle_opt_out_at FROM users WHERE email = 'reader@example.org'`).Scan(&at)
	if at == nil {
		t.Fatal("not opted out")
	}
	if res := post(token); res.StatusCode != http.StatusNoContent {
		t.Fatalf("a repeat click should also succeed: %d", res.StatusCode)
	}
	for _, bad := range []string{"", "not-a-token", "00000000-0000-0000-0000-000000000000"} {
		if res := post(bad); res.StatusCode != http.StatusBadRequest {
			t.Errorf("token %q: status = %d, want 400", bad, res.StatusCode)
		}
	}
}

// Skipping CSRF for this one endpoint must not open any other.
func TestOnlyTheUnsubscribeEndpointSkipsCSRF(t *testing.T) {
	e := newEnv(t, 1000)
	res, err := http.Post(e.srv.URL+"/api/v1/auth/logout", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without a CSRF token = %d, want 403", res.StatusCode)
	}
}

func TestEmailPreferencesAndTheWeeklySummaryLink(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "reader@example.org")
	if r := c.call("GET", "/api/v1/account/email-preferences", nil); r.Status != 200 ||
		r.Body["email_preferences"].(map[string]any)["weekly_summary"] != true || r.Body["email_preferences"].(map[string]any)["onboarding"] != true {
		t.Fatalf("both on by default: %d %v", r.Status, r.Body)
	}
	if r := e.newClient(t).call("GET", "/api/v1/account/email-preferences", nil); r.Status != 401 {
		t.Fatalf("signed out: %d", r.Status)
	}

	// The summary's one-click link, as a mail client sends it: a bare POST with the list in the address.
	var token string
	e.pool.QueryRow(context.Background(), `SELECT unsubscribe_token::text FROM users WHERE email = 'reader@example.org'`).Scan(&token)
	res, err := http.Post(e.srv.URL+"/api/v1/lifecycle/unsubscribe?token="+url.QueryEscape(token)+"&list=weekly_summary", "application/x-www-form-urlencoded",
		strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("%d", res.StatusCode)
	}
	p := c.call("GET", "/api/v1/account/email-preferences", nil).Body["email_preferences"].(map[string]any)
	if p["weekly_summary"] != false || p["onboarding"] != true {
		t.Fatalf("only the summary stops: %v", p)
	}
	if r := c.call("PUT", "/api/v1/account/email-preferences", map[string]any{"weekly_summary": true}); r.Body["email_preferences"].(map[string]any)["weekly_summary"] != true {
		t.Fatalf("turned back on: %v", r.Body)
	}
}
