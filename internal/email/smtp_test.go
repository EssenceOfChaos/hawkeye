package email

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type received struct{ From, To, Data, User string }

type fakeSMTP struct {
	ln               net.Listener
	cert             tls.Certificate
	pool             *x509.CertPool
	startTLS         bool   // advertise and accept STARTTLS
	implicit         bool   // speak TLS from the first byte
	user             string // required AUTH PLAIN credentials ("" = auth not offered)
	pass             string
	rcptCode         string // reply to RCPT TO, default "250 OK"
	mu               sync.Mutex
	got              []received
	sawAuthBeforeTLS bool
}

func newCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startFake(t *testing.T, f *fakeSMTP) *fakeSMTP {
	t.Helper()
	f.cert, f.pool = newCert(t)
	var err error
	f.ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if f.implicit {
		f.ln = tls.NewListener(f.ln, &tls.Config{Certificates: []tls.Certificate{f.cert}})
	}
	if f.rcptCode == "" {
		f.rcptCode = "250 OK"
	}
	t.Cleanup(func() { f.ln.Close() })
	go func() {
		for {
			c, err := f.ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(c)
	say := func(s string) { fmt.Fprintf(c, "%s\r\n", s) }
	say("220 fake ESMTP")
	var cur received
	tlsActive := f.implicit
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			say("250-fake")
			if f.startTLS && !tlsActive {
				say("250-STARTTLS")
			}
			if f.user != "" {
				say("250-AUTH PLAIN")
			}
			say("250 8BITMIME")
		case up == "STARTTLS":
			say("220 ready")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{f.cert}})
			if tc.Handshake() != nil {
				return
			}
			c, r, tlsActive = tc, bufio.NewReader(tc), true
			say = func(s string) { fmt.Fprintf(tc, "%s\r\n", s) }
		case strings.HasPrefix(up, "AUTH PLAIN "):
			if !tlsActive {
				f.mu.Lock()
				f.sawAuthBeforeTLS = true
				f.mu.Unlock()
			}
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN "):]))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == f.user && parts[2] == f.pass {
				cur.User = parts[1]
				say("235 authenticated")
			} else {
				say("535 5.7.8 bad credentials")
			}
		case strings.HasPrefix(up, "MAIL FROM:"):
			addr := line[len("MAIL FROM:"):]
			if i := strings.Index(addr, ">"); i >= 0 {
				addr = addr[:i] // drop ESMTP parameters such as BODY=8BITMIME
			}
			cur.From = strings.Trim(addr, "<> ")
			say("250 OK")
		case strings.HasPrefix(up, "RCPT TO:"):
			addr := line[len("RCPT TO:"):]
			if i := strings.Index(addr, ">"); i >= 0 {
				addr = addr[:i]
			}
			cur.To = strings.Trim(addr, "<> ")
			say(f.rcptCode)
		case up == "DATA":
			say("354 go")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			cur.Data = sb.String()
			f.mu.Lock()
			f.got = append(f.got, cur)
			f.mu.Unlock()
			say("250 queued")
		case up == "QUIT":
			say("221 bye")
			return
		default:
			say("250 OK")
		}
	}
}

func (f *fakeSMTP) messages() []received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]received(nil), f.got...)
}

func sender(t *testing.T, f *fakeSMTP, mode string, mutate func(*SMTPConfig)) *SMTP {
	t.Helper()
	cfg := SMTPConfig{Host: "127.0.0.1", Port: f.port(), TLS: mode, From: "HawkEye <alerts@example.com>", Username: f.user, Password: f.pass,
		TLSConfig: &tls.Config{RootCAs: f.pool}, Timeout: 5 * time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := NewSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var msg = Message{To: "user@example.org", Subject: "[HawkEye] DOWN: Client Site", Text: "Client Site is down.\n\nURL: https://client.example.com/\n"}

func TestSMTPStartTLSAuthAndMessageFormat(t *testing.T) {
	f := startFake(t, &fakeSMTP{startTLS: true, user: "apikey", pass: "s3cret"})
	if err := sender(t, f, TLSStartTLS, nil).Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	got := f.messages()
	if len(got) != 1 || got[0].From != "alerts@example.com" || got[0].To != "user@example.org" || got[0].User != "apikey" {
		t.Fatalf("%+v", got)
	}
	if f.sawAuthBeforeTLS {
		t.Fatal("credentials were offered before the connection was encrypted")
	}
	parsed, err := mail.ReadMessage(strings.NewReader(got[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	h := parsed.Header
	if h.Get("From") != `"HawkEye" <alerts@example.com>` && h.Get("From") != "HawkEye <alerts@example.com>" {
		t.Fatalf("From: %q", h.Get("From"))
	}
	if h.Get("Subject") != msg.Subject || h.Get("Content-Type") != `text/plain; charset="utf-8"` || h.Get("Message-ID") == "" || h.Get("Date") == "" || h.Get("Auto-Submitted") == "" {
		t.Fatalf("headers: %v", h)
	}
}

func TestSMTPImplicitTLS(t *testing.T) {
	f := startFake(t, &fakeSMTP{implicit: true, user: "u", pass: "p"})
	if err := sender(t, f, TLSImplicit, nil).Send(t.Context(), msg); err != nil || len(f.messages()) != 1 {
		t.Fatalf("%v %v", err, f.messages())
	}
}

func TestSMTPPlainOnlyWhenExplicitlyConfigured(t *testing.T) {
	f := startFake(t, &fakeSMTP{})
	if err := sender(t, f, TLSNone, nil).Send(t.Context(), msg); err != nil || len(f.messages()) != 1 {
		t.Fatalf("%v", err)
	}
}

func TestSMTPNeverDowngradesToPlainText(t *testing.T) {
	f := startFake(t, &fakeSMTP{startTLS: false, user: "u", pass: "p"}) // server offers no STARTTLS
	err := sender(t, f, TLSStartTLS, nil).Send(t.Context(), msg)
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("must refuse to continue unencrypted, got %v", err)
	}
	if len(f.messages()) != 0 || f.sawAuthBeforeTLS {
		t.Fatal("message or credentials were sent without encryption")
	}
	if errors.Is(err, ErrPermanent) {
		t.Fatal("a server misconfiguration is retryable, not a permanent recipient failure")
	}
}

func TestSMTPVerifiesServerCertificates(t *testing.T) {
	f := startFake(t, &fakeSMTP{startTLS: true, user: "u", pass: "p"})
	// Without trusting the test CA the self-signed certificate must be rejected.
	s := sender(t, f, TLSStartTLS, func(c *SMTPConfig) { c.TLSConfig = nil })
	if err := s.Send(t.Context(), msg); err == nil {
		t.Fatal("an untrusted certificate was accepted")
	}
	if len(f.messages()) != 0 || f.sawAuthBeforeTLS {
		t.Fatal("nothing may be sent over an unverified connection")
	}
}

func TestSMTPErrorClassification(t *testing.T) {
	cases := map[string]struct {
		f         *fakeSMTP
		permanent bool
	}{
		"mailbox does not exist (550)": {&fakeSMTP{rcptCode: "550 5.1.1 no such user"}, true},
		"content rejected (554)":       {&fakeSMTP{rcptCode: "554 5.7.1 rejected"}, true},
		"greylisted / try later (451)": {&fakeSMTP{rcptCode: "451 4.7.1 try later"}, false},
		"mailbox full (452)":           {&fakeSMTP{rcptCode: "452 4.2.2 over quota"}, false},
		"relay denied misconfig (554 is permanent but 530 auth is retryable)": {&fakeSMTP{user: "u", pass: "wrong"}, false},
	}
	for name, c := range cases {
		f := startFake(t, c.f)
		s := sender(t, f, TLSNone, func(cfg *SMTPConfig) {
			if f.user != "" {
				cfg.Password = "also-wrong" // authentication will fail with 535
			}
		})
		err := s.Send(t.Context(), msg)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if got := errors.Is(err, ErrPermanent); got != c.permanent {
			t.Errorf("%s: permanent=%v, want %v (%v)", name, got, c.permanent, err)
		}
	}
}

func TestSMTPConnectionFailuresAreRetryable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s, _ := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, TLS: TLSNone, From: "a@example.com", Timeout: 2 * time.Second})
	if err := s.Send(t.Context(), msg); err == nil || errors.Is(err, ErrPermanent) {
		t.Fatalf("connection refused must be a retryable error, got %v", err)
	}
}

func TestSMTPTimesOutOnAHungServer(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c // accept and say nothing
		}
	}()
	s, _ := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, TLS: TLSNone, From: "a@example.com", Timeout: 400 * time.Millisecond})
	start := time.Now()
	if err := s.Send(t.Context(), msg); err == nil {
		t.Fatal("expected a timeout")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("a hung server must not hold the worker: %v", time.Since(start))
	}
}

func TestSMTPCannotBeUsedToInjectHeadersOrRecipients(t *testing.T) {
	f := startFake(t, &fakeSMTP{})
	s := sender(t, f, TLSNone, nil)
	bad := []Message{
		{To: "a@example.org\r\nBcc: victim@example.net", Subject: "x", Text: "y"},
		{To: "a@example.org", Subject: "hello\r\nBcc: victim@example.net", Text: "y"},
		{To: "a@example.org", From: "evil@example.org\r\nBcc: v@example.net", Subject: "x", Text: "y"},
		{To: "not-an-address", Subject: "x", Text: "y"},
		{To: "a@example.org, b@example.org", Subject: "x", Text: "y"}, // two recipients in one field
	}
	for i, m := range bad {
		if err := s.Send(t.Context(), m); !errors.Is(err, ErrPermanent) {
			t.Errorf("case %d: want ErrPermanent, got %v", i, err)
		}
	}
	if len(f.messages()) != 0 {
		t.Fatalf("injection attempts produced mail: %+v", f.messages())
	}
}

func TestSMTPEncodesNonASCIISubjectAndBody(t *testing.T) {
	f := startFake(t, &fakeSMTP{})
	m := Message{To: "user@example.org", Subject: "Café ✔ down: 日本語", Text: "Línea con acentos y emoji ✔\n" + strings.Repeat("long line ", 40) + "\n"}
	if err := sender(t, f, TLSNone, nil).Send(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	data := f.messages()[0].Data
	for _, line := range strings.Split(data, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line exceeds the SMTP limit (%d)", len(line))
		}
	}
	parsed, _ := mail.ReadMessage(strings.NewReader(data))
	dec := new(mime.WordDecoder)
	subj, err := dec.DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subj != m.Subject {
		t.Fatalf("subject %q (%v)", subj, err)
	}
}

func TestNewSMTPValidatesConfig(t *testing.T) {
	for name, c := range map[string]SMTPConfig{
		"no host":    {From: "a@example.com"},
		"bad from":   {Host: "h", From: "nope"},
		"bad tls":    {Host: "h", From: "a@example.com", TLS: "ssl3"},
		"empty from": {Host: "h"},
	} {
		if _, err := NewSMTP(c); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	s, err := NewSMTP(SMTPConfig{Host: "h", From: "a@example.com"})
	if err != nil || s.cfg.Port != 587 || s.cfg.TLS != TLSStartTLS {
		t.Fatalf("defaults must be secure: %+v %v", s, err)
	}
}

func TestSMTPTrustsAnAdditionalCAFile(t *testing.T) {
	f := startFake(t, &fakeSMTP{startTLS: true, user: "u", pass: "p"})
	// Write the fake server's certificate as the "private CA" and rely on CAFile instead of an injected TLS config.
	pemBytes := pemEncodeCert(f.cert.Certificate[0])
	path := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: f.port(), TLS: TLSStartTLS, From: "a@example.com", Username: "u", Password: "p", CAFile: path, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(t.Context(), msg); err != nil || len(f.messages()) != 1 {
		t.Fatalf("a certificate signed by the configured CA must be accepted: %v", err)
	}
	// A bad CA file fails at startup, not at first send.
	bad := t.TempDir() + "/bad.pem"
	os.WriteFile(bad, []byte("not a certificate"), 0o600)
	if _, err := NewSMTP(SMTPConfig{Host: "h", From: "a@example.com", CAFile: bad}); err == nil {
		t.Fatal("an invalid CA file must be rejected")
	}
	if _, err := NewSMTP(SMTPConfig{Host: "h", From: "a@example.com", CAFile: "/does/not/exist.pem"}); err == nil {
		t.Fatal("a missing CA file must be rejected")
	}
}

func pemEncodeCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestOneClickUnsubscribeHeadersAreAddedOnlyWhenAsked(t *testing.T) {
	from, _ := mail.ParseAddress("HawkEye <hi@hawkeye.test>")
	to, _ := mail.ParseAddress("u@example.com")
	read := func(unsub string) *mail.Message {
		raw, err := buildMessage(from, to, nil, Message{Subject: "Hi", Text: "body", HTML: "<p>body</p>", Unsubscribe: unsub})
		if err != nil {
			t.Fatal(err)
		}
		m, err := mail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	h := read("https://app.hawkeye.test/api/v1/lifecycle/unsubscribe?token=abc").Header
	if h.Get("List-Unsubscribe") != "<https://app.hawkeye.test/api/v1/lifecycle/unsubscribe?token=abc>" || h.Get("List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers: %v", h)
	}
	h = read("").Header
	if h.Get("List-Unsubscribe") != "" || h.Get("List-Unsubscribe-Post") != "" {
		t.Fatalf("alerts and account mail must not carry unsubscribe headers: %v", h)
	}
	for _, bad := range []string{"mailto:x@y.z", "ftp://x", "https://x/\r\nBcc: evil@example.com", "https://x/ y", "https://x/>"} {
		if _, err := buildMessage(from, to, nil, Message{Subject: "Hi", Text: "body", Unsubscribe: bad}); err == nil {
			t.Errorf("accepted unsubscribe URL %q", bad)
		}
	}
}

func TestAttachmentsAreSentAsBase64PartsAfterTheBody(t *testing.T) {
	from, _ := mail.ParseAddress("HawkEye <hi@hawkeye.test>")
	to, _ := mail.ParseAddress("u@example.com")
	pdf := bytes.Repeat([]byte("%PDF-1.4 binary \x00\xff\x10 data "), 400) // longer than one base64 line, with bytes that are not text
	raw, err := buildMessage(from, to, nil, Message{Subject: "Reports", Text: "plain", HTML: "<p>html</p>",
		Attachments: []Attachment{{Filename: "report-2026-09.pdf", ContentType: "application/pdf", Data: pdf}, {Filename: "b.pdf", ContentType: "application/pdf", Data: []byte("two")}}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type = %q %v", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	body, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if bt, _, _ := mime.ParseMediaType(body.Header.Get("Content-Type")); bt != "multipart/alternative" {
		t.Fatalf("first part = %q, want the text/HTML body", bt)
	}
	var got [][]byte
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		if !strings.HasPrefix(p.Header.Get("Content-Disposition"), "attachment;") || p.Header.Get("Content-Transfer-Encoding") != "base64" {
			t.Fatalf("attachment headers: %v", p.Header)
		}
		dec, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, dec)
	}
	if len(got) != 2 || !bytes.Equal(got[0], pdf) || string(got[1]) != "two" {
		t.Fatalf("attachments did not survive: %d parts", len(got))
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("a line is %d bytes; mail servers reject lines over 998", len(line))
		}
	}
}

func TestAttachmentNamesAndTypesCannotInjectHeaders(t *testing.T) {
	from, _ := mail.ParseAddress("hi@hawkeye.test")
	to, _ := mail.ParseAddress("u@example.com")
	for _, a := range []Attachment{
		{Filename: "a.pdf\r\nBcc: evil@example.com", ContentType: "application/pdf"},
		{Filename: `a".pdf`, ContentType: "application/pdf"},
		{Filename: "../a.pdf", ContentType: "application/pdf"},
		{Filename: "", ContentType: "application/pdf"},
		{Filename: "a.pdf", ContentType: "application/pdf\r\nX: y"},
		{Filename: "a.pdf", ContentType: "pdf"},
		{Filename: "a.pdf", ContentType: "application/pdf", Data: make([]byte, MaxAttachmentBytes+1)},
	} {
		if _, err := buildMessage(from, to, nil, Message{Subject: "x", Text: "y", Attachments: []Attachment{a}}); err == nil {
			t.Errorf("accepted %q / %q (%d bytes)", a.Filename, a.ContentType, len(a.Data))
		}
	}
}
