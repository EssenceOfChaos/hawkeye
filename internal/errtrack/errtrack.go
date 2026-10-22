// Package errtrack reports unexpected errors and panics to an error-tracking service. Reporting is optional: with no
// configuration the Noop reporter is used and errors are only logged.
package errtrack

import (
	"context"
	"log/slog"
)

// Reporter receives unexpected (5xx-class) errors. Implementations must be safe for concurrent use and must never block the caller for long.
type Reporter interface {
	Capture(ctx context.Context, err error, fields map[string]string)
}

// Noop discards everything.
type Noop struct{}

func (Noop) Capture(context.Context, error, map[string]string) {}

// LogStatus says at startup whether errors are being sent anywhere and, if so, to which Sentry project, so a missing or wrong SENTRY_DSN shows up in the
// logs instead of as silence in Sentry. The DSN's key is never logged.
func LogStatus(log *slog.Logger, dsn, environment, release string) {
	if dsn == "" {
		log.Info("error tracking disabled (set SENTRY_DSN to enable it)")
		return
	}
	host, project, ok := Describe(dsn)
	if !ok {
		log.Warn("error tracking: SENTRY_DSN is set but does not look like a DSN")
		return
	}
	log.Info("error tracking enabled", "sentry_host", host, "sentry_project", project, "environment", environment, "release", release)
}
