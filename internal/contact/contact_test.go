package contact

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"hawkeye/internal/email"
	"hawkeye/internal/validation"
)

func valid() Submission {
	return Submission{Category: "billing", Name: "Ada Lovelace", Email: "ada@example.org", Subject: "Charged twice", Message: "I was charged twice this month, please check.", URL: "https://ada.example.com/"}
}

func newSvc() (*Service, *email.Memory) {
	m := &email.Memory{}
	return NewService(m, slog.New(slog.NewTextHandler(io.Discard, nil)), "support@mail.example.com", "alerts@mail.example.com", "https://app.example.com"), m
}

func TestSubmitSendsTriagedMessageToSupportWithReplyTo(t *testing.T) {
	s, m := newSvc()
	in := valid()
	in.UserID, in.Plan = "user-123", "Pro"
	ref, err := s.Submit(context.Background(), in)
	if err != nil || !strings.HasPrefix(ref, "HK-") {
		t.Fatalf("ref=%q err=%v", ref, err)
	}
	msgs := m.Messages()
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	got := msgs[0]
	if got.To != "support@mail.example.com" || got.From != "alerts@mail.example.com" || got.ReplyTo != "ada@example.org" {
		t.Fatalf("routing wrong: %+v", got)
	}
	if got.Subject != "[Billing or subscription] Charged twice ("+ref+")" {
		t.Fatalf("subject %q", got.Subject)
	}
	for _, want := range []string{"Priority: High", "Pro", "user-123", "https://ada.example.com/", "charged twice this month", ref} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text missing %q:\n%s", want, got.Text)
		}
	}
	if !strings.Contains(got.HTML, "#c62828") {
		t.Error("high-priority messages should use the alert accent")
	}
}

func TestAnonymousSenderIsLabelled(t *testing.T) {
	s, m := newSvc()
	if _, err := s.Submit(context.Background(), valid()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Messages()[0].Text, "No (anonymous visitor)") {
		t.Fatal("support should see that the sender was not signed in")
	}
}

func TestValidationReportsEveryBadField(t *testing.T) {
	in := Submission{Category: "nope", Name: " ", Email: "Ada <ada@example.org>", Subject: "x", Message: "short", URL: "javascript:alert(1)"}
	err := Validate(&in)
	ve, ok := err.(*validation.Error)
	if !ok {
		t.Fatalf("%T %v", err, err)
	}
	for _, f := range []string{"category", "name", "email", "subject", "message", "url"} {
		if ve.Fields[f] == "" {
			t.Errorf("field %q not reported: %v", f, ve.Fields)
		}
	}
}

func TestEmailMustBeABareAddress(t *testing.T) {
	for _, bad := range []string{"a@b.co, evil@x.com", "Name <a@b.co>", "a@b.co\r\nBcc: evil@x.com", "not-an-email"} {
		in := valid()
		in.Email = bad
		if Validate(&in) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestHeaderInjectionIsNeutralised(t *testing.T) {
	s, m := newSvc()
	in := valid()
	in.Subject = "Hello\r\nBcc: evil@example.com"
	in.Name = "Mallory\nX-Injected: yes"
	if _, err := s.Submit(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got := m.Messages()[0]
	if strings.ContainsAny(got.Subject, "\r\n") || got.ReplyTo != "ada@example.org" {
		t.Fatalf("%+v", got)
	}
	for _, line := range strings.Split(got.Text, "\n") {
		if strings.HasPrefix(line, "Bcc:") || strings.HasPrefix(line, "X-Injected:") {
			t.Fatalf("injected line in body: %q", line)
		}
	}
}

func TestMessageIsEscapedInHTML(t *testing.T) {
	s, m := newSvc()
	in := valid()
	in.Message = "<script>alert(1)</script> and <b>bold</b>"
	if _, err := s.Submit(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	h := m.Messages()[0].HTML
	if strings.Contains(h, "<script>") || strings.Contains(h, "<b>bold") {
		t.Fatal("visitor text was not escaped")
	}
}

func TestInvalidSubmissionSendsNothing(t *testing.T) {
	s, m := newSvc()
	in := valid()
	in.Message = ""
	if _, err := s.Submit(context.Background(), in); err == nil || len(m.Messages()) != 0 {
		t.Fatalf("err=%v sent=%d", err, len(m.Messages()))
	}
}

func TestAcknowledgementGoesToTheSender(t *testing.T) {
	s, m := newSvc()
	s.Acknowledge(context.Background(), valid(), "HK-ABC123")
	got := m.Messages()[0]
	if got.To != "ada@example.org" || !strings.Contains(got.Subject, "HK-ABC123") || got.ReplyTo != "" {
		t.Fatalf("%+v", got)
	}
}

func TestAcknowledgementEchoesNothingTheVisitorWrote(t *testing.T) {
	s, m := newSvc()
	in := valid()
	in.Name = "Your account is locked, visit https://evil.example now"
	in.Subject = "Urgent: verify your card at evil.example"
	s.Acknowledge(context.Background(), in, "HK-ABC123")
	got := m.Messages()[0]
	for _, bad := range []string{"evil.example", "account is locked", "Urgent"} {
		if strings.Contains(got.Text, bad) || strings.Contains(got.HTML, bad) || strings.Contains(got.Subject, bad) {
			t.Fatalf("visitor text %q reached the acknowledgement:\n%s", bad, got.Text)
		}
	}
}

func TestAcknowledgementRefusesAnAddressThatCouldInjectHeaders(t *testing.T) {
	s, m := newSvc()
	in := valid()
	in.Email = "ada@example.org\r\nBcc: evil@example.com"
	s.Acknowledge(context.Background(), in, "HK-ABC123")
	if len(m.Messages()) != 0 {
		t.Fatalf("sent to %q", m.Messages()[0].To)
	}
}
