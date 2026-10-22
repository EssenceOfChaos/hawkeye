package errtrack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

// Config for the Sentry reporter. An empty DSN disables reporting (New returns Noop).
type Config struct {
	DSN         string
	Environment string
	Release     string
}

var (
	emailRe  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	secretRe = regexp.MustCompile(`\b(?:sk|rk|pk|whsec)_[A-Za-z0-9_]{6,}`)
	tokenRe  = regexp.MustCompile(`(?i)(password|passwd|secret|token)(["'=:\s]+)[^\s"',;&]+`)
	// Whole header values (Bearer/Basic schemes contain a space, cookies hold several pairs) are redacted to the end of the line.
	headerRe = regexp.MustCompile(`(?i)\b(authorization|cookie|set-cookie)\b[ \t]*[:=][^\n]*`)
)

// Scrub removes personal data and credentials from text that is about to leave the process. Database errors in particular can
// echo the offending row ("Key (email)=(someone@example.com) already exists").
func Scrub(s string) string {
	s = emailRe.ReplaceAllString(s, "[email]")
	s = secretRe.ReplaceAllString(s, "[redacted]")
	s = headerRe.ReplaceAllString(s, "$1: [redacted]")
	return tokenRe.ReplaceAllString(s, "$1$2[redacted]")
}

type sentryReporter struct {
	client *sentry.Client
}

// clientOptions is the one place that decides what leaves the process: every event is scrubbed, and no user, server name or request is attached.
func clientOptions(cfg Config) sentry.ClientOptions {
	return sentry.ClientOptions{
		Dsn: cfg.DSN, Environment: cfg.Environment, Release: cfg.Release,
		AttachStacktrace: true, SendDefaultPII: false,
		// Scrub every event, including messages, exception values and breadcrumbs, before it is sent.
		BeforeSend: func(ev *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			ev.Message = Scrub(ev.Message)
			for i := range ev.Exception {
				ev.Exception[i].Value = Scrub(ev.Exception[i].Value)
			}
			ev.ServerName = ""
			ev.User = sentry.User{}
			ev.Request = nil
			return ev
		},
	}
}

// New returns a Reporter and a shutdown function that flushes pending events. With no DSN it returns Noop and a no-op shutdown.
func New(cfg Config) (Reporter, func(), error) {
	if cfg.DSN == "" {
		return Noop{}, func() {}, nil
	}
	client, err := sentry.NewClient(clientOptions(cfg))
	if err != nil {
		return nil, nil, fmt.Errorf("sentry: %w", err)
	}
	return &sentryReporter{client: client}, func() { client.Flush(3 * time.Second) }, nil
}

// Describe names where events go, for the startup log: the ingest host and the project number from the DSN. The key in the DSN is not included.
// ok is false for an empty or malformed DSN.
func Describe(dsn string) (host, project string, ok bool) {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return "", "", false
	}
	return u.Hostname(), path.Base(u.Path), true
}

// lockedBuffer collects the SDK's diagnostics from its sender goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestReport is the outcome of SendTest.
type TestReport struct {
	EventID string
	// Delivered is true when the event was sent and the SDK reported no problem doing so.
	Delivered bool
	// Problems holds the SDK's diagnostic lines about a failed or refused delivery, scrubbed of the DSN key.
	Problems []string
}

// problemPhrases are what the SDK writes to its debug log when a delivery goes wrong. The log also has ordinary lines ("Sending error [id] to host"),
// so matching on the word "error" alone would call every delivery a failure.
var problemPhrases = []string{"there was an issue", "failed", "error sending", "dropped", "too many requests", "timed out", "canceled", "rate limit"}

func isProblem(lowerLine string) bool {
	for _, p := range problemPhrases {
		if strings.Contains(lowerLine, p) {
			return true
		}
	}
	return false
}

// SendTest sends one clearly labelled test event and waits up to timeout for it to be delivered. It is for checking a new DSN: the normal Reporter sends
// in the background and never says whether Sentry accepted anything.
func SendTest(cfg Config, message string, timeout time.Duration) (TestReport, error) {
	if cfg.DSN == "" {
		return TestReport{}, errors.New("no DSN is set")
	}
	diag := &lockedBuffer{}
	opts := clientOptions(cfg)
	opts.Debug, opts.DebugWriter = true, diag
	client, err := sentry.NewClient(opts)
	if err != nil {
		return TestReport{}, fmt.Errorf("sentry: %w", err)
	}
	scope := sentry.NewScope()
	scope.SetTags(map[string]string{"component": "sentry-test"})
	id := client.CaptureException(errors.New(message), &sentry.EventHint{}, scope)
	flushed := client.Flush(timeout)

	rep := TestReport{}
	if id != nil {
		rep.EventID = string(*id)
	}
	key := ""
	if u, err := url.Parse(cfg.DSN); err == nil && u.User != nil {
		key = u.User.Username()
	}
	for _, line := range strings.Split(diag.String(), "\n") {
		l := strings.ToLower(line)
		if !isProblem(l) {
			continue
		}
		if key != "" {
			line = strings.ReplaceAll(line, key, "[key]")
		}
		rep.Problems = append(rep.Problems, strings.TrimSpace(Scrub(line)))
	}
	if !flushed {
		rep.Problems = append(rep.Problems, "the event was not delivered within the timeout")
	}
	rep.Delivered = id != nil && flushed && len(rep.Problems) == 0
	return rep, nil
}

// Capture sends err with the given tags. It does not block: the SDK sends asynchronously.
func (s *sentryReporter) Capture(_ context.Context, err error, fields map[string]string) {
	if err == nil {
		return
	}
	scope := sentry.NewScope()
	scope.SetTags(fields)
	s.client.CaptureException(err, &sentry.EventHint{}, scope)
}
