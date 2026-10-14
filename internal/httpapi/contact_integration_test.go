package httpapi_test

import (
	"strings"
	"testing"
)

func contactBody(over map[string]any) map[string]any {
	b := map[string]any{"category": "technical", "name": "Visitor", "email": "visitor@example.org", "subject": "Alerts are not arriving",
		"message": "My monitor went down yesterday but I never received an email.", "url": "https://visitor.example.com/"}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func (e *env) mailsTo(addr string) (n int) {
	for _, m := range e.mail.Messages() {
		if m.To == addr {
			n++
		}
	}
	return n
}

func TestAnyoneCanContactSupport(t *testing.T) {
	e := newEnv(t, 1000)
	c := e.newClient(t)
	if r := c.call("GET", "/api/v1/contact/categories", nil); r.Status != 200 {
		t.Fatalf("categories: %d", r.Status)
	}
	r := c.call("POST", "/api/v1/contact", contactBody(nil))
	if r.Status != 202 || !strings.HasPrefix(r.Body["reference"].(string), "HK-") {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if e.mailsTo("support@test.example") != 1 || e.mailsTo("visitor@example.org") != 1 {
		t.Fatalf("expected one message to support and one acknowledgement, got %d / %d", e.mailsTo("support@test.example"), e.mailsTo("visitor@example.org"))
	}
	for _, m := range e.mail.Messages() {
		if m.To == "support@test.example" && m.ReplyTo != "visitor@example.org" {
			t.Fatalf("reply-to = %q", m.ReplyTo)
		}
	}
}

func TestSignedInSenderContextIsAttachedByTheServer(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "member@example.org")
	const addr = "member@example.org"
	// The body claims to be on the Agency plan; only the server's own lookup may decide that.
	r := c.call("POST", "/api/v1/contact", contactBody(map[string]any{"email": addr, "plan": "Agency", "user_id": "forged"}))
	if r.Status != 202 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	var support string
	for _, m := range e.mail.Messages() {
		if m.To == "support@test.example" {
			support = m.Text
		}
	}
	if !strings.Contains(support, "Plan: Free") || strings.Contains(support, "forged") || strings.Contains(support, "Agency") {
		t.Fatalf("support email:\n%s", support)
	}
}

func TestContactValidationAndHoneypot(t *testing.T) {
	e := newEnv(t, 1000)
	c := e.newClient(t)
	if r := c.call("POST", "/api/v1/contact", contactBody(map[string]any{"message": "hi", "email": "bad"})); r.Status != 400 || r.Body["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	before := len(e.mail.Messages())
	// A bot that fills the hidden field is told it worked, but nothing is sent.
	if r := c.call("POST", "/api/v1/contact", contactBody(map[string]any{"company": "Spam Inc"})); r.Status != 202 {
		t.Fatalf("%d", r.Status)
	}
	if len(e.mail.Messages()) != before {
		t.Fatal("honeypot submission sent mail")
	}
}

func TestContactIsRateLimitedPerIPAndAckPerAddress(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{shared: true, emailPerH: 2})
	c := e.newClient(t)
	var codes []int
	for range 7 {
		codes = append(codes, c.call("POST", "/api/v1/contact", contactBody(nil)).Status)
	}
	ok, limited := 0, 0
	for _, s := range codes {
		switch s {
		case 202:
			ok++
		case 429:
			limited++
		}
	}
	if ok != 5 || limited != 2 {
		t.Fatalf("per-IP hourly cap of 5 not enforced: %v", codes)
	}
	// Support receives every accepted message, but the visitor's address gets at most its hourly budget of acknowledgements.
	if e.mailsTo("support@test.example") != 5 || e.mailsTo("visitor@example.org") != 2 {
		t.Fatalf("support=%d acks=%d", e.mailsTo("support@test.example"), e.mailsTo("visitor@example.org"))
	}
}
