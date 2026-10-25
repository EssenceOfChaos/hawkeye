// Package email defines the transactional email interface and development/test senders.
package email

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"sync"
)

type Message struct {
	From    string // optional; providers fall back to their configured sender
	To      string
	Subject string
	Text    string // always required: the plain-text body (and the alternative when HTML is set)
	HTML    string // optional branded body; sent as multipart/alternative
	ReplyTo string // optional address replies should go to (e.g. the person who filled in the contact form)

	// Unsubscribe is set on mail the recipient can opt out of (onboarding nudges, not alerts or security mail). It must be an http(s) URL that
	// unsubscribes with a bare POST; the sender adds List-Unsubscribe and List-Unsubscribe-Post (RFC 8058), which mail clients show as an "Unsubscribe" button.
	Unsubscribe string

	// Attachments are sent as files with the message (monthly PDF reports). Senders refuse names and types outside a small safe set (see ValidAttachment)
	// and bodies over MaxAttachmentBytes in total.
	Attachments []Attachment
}

// Attachment is one file sent with a message.
type Attachment struct {
	Filename    string // letters, digits, dot, dash and underscore only
	ContentType string // e.g. application/pdf
	Data        []byte
}

// MaxAttachmentBytes is the most attachment data one message may carry. Providers accept about 40 MB per message; this leaves a wide margin and
// stops a bug from building an enormous email.
const MaxAttachmentBytes = 20 << 20

var (
	attachmentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	attachmentType = regexp.MustCompile(`^[a-z]+/[a-z0-9.+-]+$`)
)

// ValidateAttachments says why a message's attachments cannot be sent, or returns nil. The checks keep a filename or type from adding headers or
// tricking a mail client.
func ValidateAttachments(as []Attachment) error {
	total := 0
	for _, a := range as {
		if !attachmentName.MatchString(a.Filename) {
			return errors.New("invalid attachment filename")
		}
		if !attachmentType.MatchString(a.ContentType) {
			return errors.New("invalid attachment content type")
		}
		total += len(a.Data)
	}
	if total > MaxAttachmentBytes {
		return errors.New("attachments are too large")
	}
	return nil
}

// ErrPermanent marks a delivery failure that will never succeed on retry (invalid address, rejected
// content). Senders wrap it: fmt.Errorf("%w: ...", email.ErrPermanent). Anything else is treated as transient.
var ErrPermanent = errors.New("permanent email failure")

// Sender delivers a message. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Console logs messages instead of sending them. Development only: it logs links containing secrets.
type Console struct{ Log *slog.Logger }

func (c Console) Send(ctx context.Context, m Message) error {
	c.Log.InfoContext(ctx, "email (console provider, not delivered)", "to", m.To, "subject", m.Subject, "body", m.Text)
	return nil
}

// Memory records messages; used by tests.
type Memory struct {
	mu       sync.Mutex
	messages []Message
}

func (m *Memory) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return nil
}

func (m *Memory) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.messages...)
}
