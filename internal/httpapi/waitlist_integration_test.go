package httpapi_test

import (
	"regexp"
	"strings"
	"testing"
)

var (
	waitlistConfirmRe = regexp.MustCompile(`/waitlist/confirm\?token=([A-Za-z0-9_-]+)`)
	waitlistLeaveRe   = regexp.MustCompile(`/waitlist/leave\?token=([0-9a-f-]{36})`)
)

func waitlistMail(t *testing.T, e *env, to string) (confirm, leave string, n int) {
	t.Helper()
	for _, m := range e.mail.Messages() {
		if m.To == to && strings.Contains(m.Subject, "tell me when HawkEye launches") {
			n++
			if c := waitlistConfirmRe.FindStringSubmatch(m.Text); c != nil {
				confirm = c[1]
			}
			if l := waitlistLeaveRe.FindStringSubmatch(m.Text); l != nil {
				leave = l[1]
			}
		}
	}
	return confirm, leave, n
}

func waitlistRow(t *testing.T, e *env, feature, addr string) (exists, confirmed bool) {
	t.Helper()
	err := e.pool.QueryRow(t.Context(), `SELECT true, confirmed_at IS NOT NULL FROM waitlist_entries WHERE feature = $1 AND email = $2`, feature, addr).Scan(&exists, &confirmed)
	if err != nil {
		return false, false
	}
	return exists, confirmed
}

func TestWaitlistSignUpConfirmAndLeave(t *testing.T) {
	e := newEnv(t, 1000)
	anon := e.newClient(t)

	r := anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "  Visitor@Example.com "})
	if r.Status != 202 || r.Body["status"] != "check_email" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if exists, confirmed := waitlistRow(t, e, "status_page_domains", "visitor@example.com"); !exists || confirmed {
		t.Fatal("an address typed in while signed out waits for confirmation")
	}
	confirm, leave, n := waitlistMail(t, e, "visitor@example.com")
	if n != 1 || confirm == "" || leave == "" {
		t.Fatalf("one confirmation email with both links, got %d (%q %q)", n, confirm, leave)
	}
	var stored int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM waitlist_entries WHERE position($1::bytea in confirm_token_hash) > 0`, []byte(confirm)).Scan(&stored)
	if stored != 0 {
		t.Fatal("only a hash of the confirmation token is stored")
	}

	if r := anon.call("POST", "/api/v1/waitlist/confirm", map[string]any{"token": confirm}); r.Status != 200 || r.Body["feature"] != "status pages on your own domain" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if _, confirmed := waitlistRow(t, e, "status_page_domains", "visitor@example.com"); !confirmed {
		t.Fatal("confirmed")
	}
	if r := anon.call("POST", "/api/v1/waitlist/confirm", map[string]any{"token": confirm}); r.Status != 400 || errCode(r) != "INVALID_TOKEN" {
		t.Fatalf("a link works once: %d %v", r.Status, r.Body)
	}

	// Signing up again looks the same from outside, but sends nothing: the answer never says who is on a list.
	if r := anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "visitor@example.com"}); r.Status != 202 || r.Body["status"] != "check_email" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if _, _, n := waitlistMail(t, e, "visitor@example.com"); n != 1 {
		t.Fatalf("no second email to a confirmed address, got %d", n)
	}

	// The link in the email takes the address off the list.
	if r := anon.call("POST", "/api/v1/waitlist/leave", map[string]any{"token": leave}); r.Status != 204 {
		t.Fatalf("%d", r.Status)
	}
	if exists, _ := waitlistRow(t, e, "status_page_domains", "visitor@example.com"); exists {
		t.Fatal("removed")
	}
	if r := anon.call("POST", "/api/v1/waitlist/leave", map[string]any{"token": "not-a-token"}); r.Status != 204 {
		t.Fatalf("unknown links are harmless: %d", r.Status)
	}
}

func TestWaitlistRefusesBadInputAndBots(t *testing.T) {
	e := newEnv(t, 1000)
	anon := e.newClient(t)
	if r := anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "time_travel", "email": "a@example.com"}); r.Status != 400 {
		t.Fatalf("unknown feature: %d", r.Status)
	}
	// A feature that has launched is no longer on the list (multi-step API checks, since migration 000039).
	if r := anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "api_workflows", "email": "a@example.com"}); r.Status != 400 {
		t.Fatalf("launched feature: %d", r.Status)
	}
	if r := anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "not-an-address"}); r.Status != 400 {
		t.Fatalf("bad address: %d", r.Status)
	}
	if r := anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "bot@example.com", "company": "Spam Inc"}); r.Status != 202 {
		t.Fatalf("bots get the usual answer: %d", r.Status)
	}
	if exists, _ := waitlistRow(t, e, "status_page_domains", "bot@example.com"); exists {
		t.Fatal("but nothing is stored")
	}
	if _, _, n := waitlistMail(t, e, "bot@example.com"); n != 0 {
		t.Fatal("or sent")
	}
}

func TestWaitlistAddsVerifiedSignedInPeopleAtOnce(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "member@example.com")

	// Not verified yet: they confirm by email like anyone else.
	if r := c.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "member@example.com"}); r.Status != 202 || r.Body["status"] != "check_email" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	e.markVerified(t, "member@example.com")
	if r := c.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_subscribers"}); r.Status != 200 || r.Body["status"] != "added" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if _, confirmed := waitlistRow(t, e, "status_page_subscribers", "member@example.com"); !confirmed {
		t.Fatal("confirmed at once")
	}
	if r := c.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_subscribers"}); r.Body["status"] != "already" {
		t.Fatalf("%v", r.Body)
	}
	// A pending sign-up for the same address is confirmed by signing up while verified.
	if r := c.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains"}); r.Body["status"] != "added" {
		t.Fatalf("%v", r.Body)
	}
	// Typing someone else's address still needs that person to confirm.
	if r := c.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "colleague@example.com"}); r.Body["status"] != "check_email" {
		t.Fatalf("%v", r.Body)
	}

	// The account export lists them; deleting the account removes them, including ones made with its address while signed out. (There are two
	// features, so the domains sign-up is made again, signed out, to have one of each kind.)
	mustExec(t, e, `DELETE FROM waitlist_entries WHERE email = 'member@example.com' AND feature = 'status_page_domains'`)
	anon := e.newClient(t)
	if r := anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "member@example.com"}); r.Body["status"] != "check_email" {
		t.Fatalf("%v", r.Body)
	}
	exp := c.call("GET", "/api/v1/account/export", nil)
	if wl, _ := exp.Body["waitlists"].([]any); len(wl) != 2 {
		t.Fatalf("export: %v", exp.Body["waitlists"])
	}
	if r := c.call("POST", "/api/v1/account/delete", map[string]any{"password": pw, "confirm_email": "member@example.com"}); r.Status != 204 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	var left int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM waitlist_entries WHERE email = 'member@example.com'`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d entries left after deleting the account", left)
	}
}

func TestWaitlistEmailBudgetPerAddress(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{shared: true, emailPerH: 2})
	anon := e.newClient(t)
	for i := 0; i < 5; i++ {
		if r := anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "target@example.com"}); r.Status != 202 {
			t.Fatalf("%d", r.Status)
		}
	}
	if _, _, n := waitlistMail(t, e, "target@example.com"); n != 2 {
		t.Fatalf("an address gets at most the hourly budget of confirmation emails, got %d", n)
	}
	// Unconfirmed sign-ups are dropped once their link expires.
	mustExec(t, e, `UPDATE waitlist_entries SET confirm_expires_at = now() - interval '1 minute'`)
	anon.call("POST", "/api/v1/waitlist", map[string]any{"feature": "status_page_domains", "email": "other@example.com"})
	if exists, _ := waitlistRow(t, e, "status_page_domains", "target@example.com"); exists {
		t.Fatal("an expired, unconfirmed sign-up is removed")
	}
}
