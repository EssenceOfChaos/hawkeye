// Package contact turns the public "Contact us" form into a triaged email to the support inbox.
package contact

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"hawkeye/internal/email"
	"hawkeye/internal/validation"
)

// Category is what the sender needs help with; it decides the subject tag and the priority support sees.
type Category struct {
	Code     string `json:"code"`
	Label    string `json:"label"`
	Hint     string `json:"hint"`
	Priority string `json:"priority"`
}

// Categories are ordered for display. Billing and account/privacy requests are high priority (money, and legal deadlines).
var Categories = []Category{
	{"technical", "Something isn't working", "A monitor, check, alert or the dashboard is behaving unexpectedly.", "High"},
	{"billing", "Billing or subscription", "Payments, invoices, upgrades, downgrades or cancellations.", "High"},
	{"account", "Account, data or privacy", "Sign-in problems, data export or deletion, privacy questions.", "High"},
	{"sales", "Plans, agencies and sales", "Questions before you buy, larger plans or agency needs.", "Normal"},
	{"bug", "Report a bug", "You found something broken or incorrect.", "Normal"},
	{"feature", "Suggest a feature", "Something HawkEye should do that it doesn't yet.", "Low"},
	{"other", "Something else", "Anything that doesn't fit above.", "Low"},
}

func lookup(code string) (Category, bool) {
	for _, c := range Categories {
		if c.Code == code {
			return c, true
		}
	}
	return Category{}, false
}

// Limits on what we accept (the HTTP layer also caps the request body).
const (
	MaxName    = 100
	MaxSubject = 150
	MaxMessage = 5000
	MaxURL     = 2048
	MinMessage = 10
)

// Submission is the validated content of one contact request, plus what the server knows about the sender.
type Submission struct {
	Category string
	Name     string
	Email    string
	Subject  string
	Message  string
	URL      string // optional: the website the request is about; shown to support, never fetched

	// Server-side context, never taken from the request body.
	UserID, Plan string // set only when the sender is signed in
}

// Service sends contact requests to the support inbox.
type Service struct {
	mail       email.Sender
	log        *slog.Logger
	to         string // support inbox
	from       string // our sender address
	appBaseURL string
}

func NewService(mail email.Sender, log *slog.Logger, supportAddress, from, appBaseURL string) *Service {
	return &Service{mail: mail, log: log, to: supportAddress, from: from, appBaseURL: appBaseURL}
}

// Validate cleans the submission in place and reports every invalid field.
func Validate(in *Submission) error {
	f := map[string]string{}
	in.Name = oneLine(in.Name)
	in.Subject = oneLine(in.Subject)
	in.Email = strings.TrimSpace(in.Email)
	in.Message = strings.TrimSpace(normalizeNewlines(stripControl(in.Message, true)))
	in.URL = strings.TrimSpace(in.URL)
	if _, ok := lookup(in.Category); !ok {
		f["category"] = "Choose what your message is about."
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > MaxName {
		f["name"] = fmt.Sprintf("Enter your name (up to %d characters).", MaxName)
	}
	if a, err := mail.ParseAddress(in.Email); err != nil || a.Address != in.Email || len(in.Email) > 254 {
		f["email"] = "Enter a valid email address so we can reply."
	}
	if n := utf8.RuneCountInString(in.Subject); n < 3 || n > MaxSubject {
		f["subject"] = fmt.Sprintf("Enter a subject (3 to %d characters).", MaxSubject)
	}
	if n := utf8.RuneCountInString(in.Message); n < MinMessage || n > MaxMessage {
		f["message"] = fmt.Sprintf("Write a message of %d to %d characters.", MinMessage, MaxMessage)
	}
	if in.URL != "" {
		if u, err := url.Parse(in.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(in.URL) > MaxURL {
			f["url"] = "Enter a full website address starting with http:// or https://."
		}
	}
	if len(f) > 0 {
		return &validation.Error{Fields: f}
	}
	return nil
}

// Submit validates the request, emails it to support (Reply-To is the sender, so replying answers them) and returns a reference
// the sender can quote. The support email is the important one: if it fails the caller must report failure.
func (s *Service) Submit(ctx context.Context, in Submission) (string, error) {
	if err := Validate(&in); err != nil {
		return "", err
	}
	cat, _ := lookup(in.Category)
	ref := newRef()

	facts := []email.Fact{
		{Label: "Reference", Value: ref},
		{Label: "Category", Value: cat.Label},
		{Label: "Priority", Value: cat.Priority},
		{Label: "From", Value: in.Name + " <" + in.Email + ">"},
	}
	if in.URL != "" {
		facts = append(facts, email.Fact{Label: "Website", Value: in.URL})
	}
	if in.UserID != "" {
		facts = append(facts, email.Fact{Label: "Signed-in account", Value: in.UserID}, email.Fact{Label: "Plan", Value: in.Plan})
	} else {
		facts = append(facts, email.Fact{Label: "Signed-in account", Value: "No (anonymous visitor)"})
	}
	facts = append(facts, email.Fact{Label: "Received", Value: time.Now().UTC().Format("2006-01-02 15:04 UTC")})

	c := email.Content{
		Preheader: in.Subject, Heading: in.Subject,
		Paragraphs: []string{"New message from the HawkEye contact form. Reply to this email to answer the sender directly."},
		Facts:      facts, Quote: in.Message, AppBaseURL: s.appBaseURL,
	}
	if cat.Priority == "High" {
		c.Tone = email.ToneAlert
	}
	m := c.Message(s.from, s.to, fmt.Sprintf("[%s] %s (%s)", cat.Label, in.Subject, ref))
	m.ReplyTo = in.Email
	if err := s.mail.Send(ctx, m); err != nil {
		return "", fmt.Errorf("send contact request: %w", err)
	}
	return ref, nil
}

// Acknowledge tells the sender their message arrived. It is best-effort: failure is logged, never returned.
//
// The recipient is an address the visitor typed, possibly someone else's, so this email carries nothing the visitor wrote except that address:
// echoing their name or subject would let anyone send a victim a genuine-looking HawkEye email with text of their choosing (phishing).
// The submission is cleaned again here, so a caller that did not validate it cannot put control characters into the recipient.
func (s *Service) Acknowledge(ctx context.Context, in Submission, ref string) {
	if err := Validate(&in); err != nil {
		s.log.WarnContext(ctx, "contact acknowledgement skipped: invalid submission", "error", err)
		return
	}
	cat, _ := lookup(in.Category)
	c := email.Content{
		Preheader: "We received your message and will reply by email.", Heading: "We received your message",
		Paragraphs: []string{
			"Thanks for contacting HawkEye. We have your message and will reply to this email address.",
			"Your reference is " + ref + ". Please quote it if you write to us again about this.",
		},
		Facts: []email.Fact{{Label: "Topic", Value: cat.Label}, {Label: "Reference", Value: ref}},
		Note:  "You are receiving this because someone submitted the contact form with this address. If that was not you, you can ignore this email.", AppBaseURL: s.appBaseURL,
	}
	if err := s.mail.Send(ctx, c.Message(s.from, in.Email, "We received your message to HawkEye ("+ref+")")); err != nil {
		s.log.WarnContext(ctx, "send contact acknowledgement failed", "error", err)
	}
}

func newRef() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "HK-" + strings.ToUpper(hex.EncodeToString(b[:]))
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// stripControl removes control characters (keeping newlines and tabs when allowed) so values cannot inject email headers.
func stripControl(s string, keepNewlines bool) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			if keepNewlines {
				return r
			}
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func oneLine(s string) string { return strings.Join(strings.Fields(stripControl(s, false)), " ") }
