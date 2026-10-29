// Package errtrack reports unexpected errors and panics to an error-tracking service. Reporting is optional: with no
// configuration the Noop reporter is used and errors are only logged.
package errtrack

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Reporter receives unexpected (5xx-class) errors. Implementations must be safe for concurrent use and must never block the caller for long.
type Reporter interface {
	Capture(ctx context.Context, err error, fields map[string]string)
}

// Notifier is implemented by reporters that can also send a plain informational message that is not an error. It is separate from Reporter so that code
// which only reports errors, and the fakes that stand in for it, need not know about it.
type Notifier interface {
	// Notify sends message at the "info" level. Events with the same fingerprint are grouped into one issue; tags are attached to the event.
	Notify(ctx context.Context, message string, fingerprint []string, tags map[string]string)
}

// StartMessage is the text sent when a process starts, so Sentry shows which version began running and when.
func StartMessage(version string, at time.Time) string {
	return fmt.Sprintf("Hawkeye version %s is being deployed at %s", version, at.UTC().Format(time.RFC3339))
}

// AnnounceStart tells error tracking that a process (component: "api" or "worker") has started running version. It is how a new deploy, a rollback or a
// restart shows up in Sentry, and it proves that this process can reach Sentry at all: without it a healthy system sends nothing, which looks the same as a
// broken setup. Events are grouped by version, so each version that has run is one issue and every start of it adds an event with its time. It does nothing
// when error tracking is off.
func AnnounceStart(r Reporter, component, version string, at time.Time) {
	if n, ok := r.(Notifier); ok {
		n.Notify(context.Background(), StartMessage(version, at), []string{"start", version},
			map[string]string{"event": "start", "component": component, "version": version})
	}
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
