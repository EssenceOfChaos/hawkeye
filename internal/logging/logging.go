// Package logging builds the process-wide structured logger.
package logging

import (
	"io"
	"log/slog"
)

// New returns a JSON logger in production and a human-readable one otherwise.
func New(w io.Writer, level slog.Level, production bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if production {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
