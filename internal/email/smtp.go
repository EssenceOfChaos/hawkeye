package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"
)

// SMTP security modes.
const (
	TLSStartTLS = "starttls" // connect in plain text, then REQUIRE an upgrade (587)
	TLSImplicit = "implicit" // TLS from the first byte (465)
	TLSNone     = "none"     // no encryption: only for a local relay or tests; refused in production by config validation
)

// SMTPConfig configures the SMTP sender. It works with any SMTP provider (Amazon SES, Postmark, SendGrid, Mailgun, ...).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      string
	From     string        // default sender (an address, optionally "Name <addr>")
	Timeout  time.Duration // whole-conversation limit; default 20s

	// CAFile is an optional PEM file of additional certificate authorities to trust (an internal mail relay with a private CA).
	// Certificates are always verified; this only ADDS trust anchors to the system pool.
	CAFile string

	// TLSConfig overrides certificate verification settings (tests only).
	TLSConfig *tls.Config
}

// SMTP sends mail through an SMTP server over a fresh connection per message (volume is low; this avoids stale connections).
type SMTP struct{ cfg SMTPConfig }

func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if cfg.Host == "" {
		return nil, errors.New("smtp: host is required")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	switch cfg.TLS {
	case "":
		cfg.TLS = TLSStartTLS
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return nil, fmt.Errorf("smtp: unknown TLS mode %q (want starttls, implicit or none)", cfg.TLS)
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("smtp: invalid From address %q", cfg.From)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.CAFile != "" && cfg.TLSConfig == nil {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("smtp: read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("smtp: CA file contains no valid PEM certificates")
		}
		cfg.TLSConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &SMTP{cfg: cfg}, nil
}

// permanentCodes are SMTP replies that will not succeed on retry (bad recipient, rejected content).
// Authentication failures (530/535) are deliberately NOT here: they are a configuration problem that an operator can fix, and
// retrying lets queued alerts go out once they do.
var permanentCodes = map[int]bool{550: true, 551: true, 552: true, 553: true, 554: true, 556: true}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	from := m.From
	if from == "" {
		from = s.cfg.From
	}
	fromAddr, err := parseOne(from)
	if err != nil {
		return fmt.Errorf("%w: invalid sender", ErrPermanent)
	}
	toAddr, err := parseOne(m.To)
	if err != nil {
		return fmt.Errorf("%w: invalid recipient", ErrPermanent)
	}
	body, err := buildMessage(fromAddr, toAddr, m.Subject, m.Text)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}

	deadline := time.Now().Add(s.cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.deliver(ctx, deadline, fromAddr.Address, toAddr.Address, body); err != nil {
		var tpErr *textproto.Error
		if errors.As(err, &tpErr) && permanentCodes[tpErr.Code] {
			return fmt.Errorf("%w: %v", ErrPermanent, err)
		}
		return err // network problems, 4xx replies and auth failures are retried
	}
	return nil
}

func (s *SMTP) tlsConfig() *tls.Config {
	if s.cfg.TLSConfig != nil {
		c := s.cfg.TLSConfig.Clone()
		if c.ServerName == "" {
			c.ServerName = s.cfg.Host
		}
		return c
	}
	return &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
}

func (s *SMTP) deliver(ctx context.Context, deadline time.Time, from, to string, msg []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	d := &net.Dialer{Deadline: deadline}
	var conn net.Conn
	var err error
	if s.cfg.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: d, Config: s.tlsConfig()}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()

	if s.cfg.TLS == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			// Never fall back to plain text: credentials and message content would travel unencrypted.
			return errors.New("smtp: server does not offer STARTTLS; refusing to continue unencrypted")
		}
		if err := c.StartTLS(s.tlsConfig()); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		// PlainAuth itself refuses to send credentials over an unencrypted non-local connection.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp end of data: %w", err)
	}
	_ = c.Quit()
	return nil
}

func parseOne(s string) (*mail.Address, error) {
	if strings.ContainsAny(s, "\r\n") {
		return nil, errors.New("line break in address")
	}
	return mail.ParseAddress(s)
}

// buildMessage renders a plain-text RFC 5322 message. Header values are validated (no CR/LF) so a monitor name or recipient can
// never inject headers, the subject is MIME-encoded when needed, and the body is quoted-printable UTF-8.
func buildMessage(from, to *mail.Address, subject, text string) ([]byte, error) {
	if strings.ContainsAny(subject, "\r\n") {
		return nil, errors.New("line break in subject")
	}
	var id [12]byte
	_, _ = rand.Read(id[:])
	domain := "localhost"
	if i := strings.LastIndex(from.Address, "@"); i >= 0 {
		domain = from.Address[i+1:]
	}

	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from.String())
	h("To", to.String())
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", time.Now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id[:])+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", `text/plain; charset="utf-8"`)
	h("Content-Transfer-Encoding", "quoted-printable")
	h("Auto-Submitted", "auto-generated") // transactional, so auto-responders stay quiet
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(text, "\r\n", "\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
