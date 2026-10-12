// Package email defines the transactional email interface and development/test senders.
package email

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

type Message struct {
	From    string // optional; providers fall back to their configured sender
	To      string
	Subject string
	Text    string
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
