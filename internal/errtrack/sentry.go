package errtrack

import (
	"context"
	"fmt"
	"regexp"
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

// New returns a Reporter and a shutdown function that flushes pending events. With no DSN it returns Noop and a no-op shutdown.
func New(cfg Config) (Reporter, func(), error) {
	if cfg.DSN == "" {
		return Noop{}, func() {}, nil
	}
	client, err := sentry.NewClient(sentry.ClientOptions{
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
	})
	if err != nil {
		return nil, nil, fmt.Errorf("sentry: %w", err)
	}
	return &sentryReporter{client: client}, func() { client.Flush(3 * time.Second) }, nil
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
