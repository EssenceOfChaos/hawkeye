package email

import (
	"fmt"
	"log/slog"
)

// New builds the configured sender: "console" (development: logs messages) or "smtp".
func New(provider string, smtp SMTPConfig, log *slog.Logger) (Sender, error) {
	switch provider {
	case "", "console":
		return Console{Log: log}, nil
	case "smtp":
		return NewSMTP(smtp)
	}
	return nil, fmt.Errorf("unknown email provider %q", provider)
}
