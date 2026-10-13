// Package errtrack reports unexpected errors and panics to an error-tracking service. Reporting is optional: with no
// configuration the Noop reporter is used and errors are only logged.
package errtrack

import "context"

// Reporter receives unexpected (5xx-class) errors. Implementations must be safe for concurrent use and must never block the caller for long.
type Reporter interface {
	Capture(ctx context.Context, err error, fields map[string]string)
}

// Noop discards everything.
type Noop struct{}

func (Noop) Capture(context.Context, error, map[string]string) {}
