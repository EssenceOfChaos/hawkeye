package email

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestLiveSMTPSend sends one real message through the configured SMTP provider (for example Resend). It is skipped unless
// SMTP_LIVE_SMOKE=1, so CI and ordinary runs never send mail. Settings come from the same SMTP_* variables the app uses, plus
// EMAIL_FROM_ADDRESS and SMTP_TEST_TO (the recipient; use your own address).
//
//	set -a; . ./.env.production; set +a; SMTP_LIVE_SMOKE=1 SMTP_TEST_TO=you@example.com go test ./internal/email -run Live -v
func TestLiveSMTPSend(t *testing.T) {
	if os.Getenv("SMTP_LIVE_SMOKE") != "1" {
		t.Skip("set SMTP_LIVE_SMOKE=1 to send a real email")
	}
	to := os.Getenv("SMTP_TEST_TO")
	if to == "" {
		t.Fatal("SMTP_TEST_TO is required")
	}
	port, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
	s, err := NewSMTP(SMTPConfig{
		Host: os.Getenv("SMTP_HOST"), Port: port, Username: os.Getenv("SMTP_USERNAME"), Password: os.Getenv("SMTP_PASSWORD"),
		TLS: os.Getenv("SMTP_TLS"), From: os.Getenv("EMAIL_FROM_ADDRESS"), Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c := Content{
		Preheader: "HawkEye email delivery test.", Heading: "HawkEye email test",
		Paragraphs:  []string{"This message was sent by HawkEye's SMTP sender to confirm that email delivery is configured.", "If you received it, verification, password-reset and alert emails will be delivered the same way."},
		Facts:       []Fact{{Label: "Sender", Value: os.Getenv("EMAIL_FROM_ADDRESS")}, {Label: "SMTP host", Value: os.Getenv("SMTP_HOST")}},
		ButtonLabel: "Open HawkEye", ButtonURL: os.Getenv("APP_BASE_URL"), AppBaseURL: os.Getenv("APP_BASE_URL"),
	}
	m := c.Message("", to, "HawkEye email test")
	m.ReplyTo = to // also proves the Reply-To header is accepted
	if err := s.Send(ctx, m); err != nil {
		t.Fatalf("send failed: %v", err)
	}
}
