package notifications

import (
	"fmt"
	"strings"
	"time"

	"hawkeye/internal/email"
)

// Alert is the data an alert email is built from.
type Alert struct {
	Event       string
	MonitorName string
	MonitorURL  string
	Cause       string
	StartedAt   time.Time
	ResolvedAt  *time.Time
	AppBaseURL  string
	From        string
	To          string
}

// clean removes control characters (so values cannot inject email headers or fake lines) and bounds the length.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 120 {
		out = out[:120] + "…"
	}
	return out
}

// Duration renders a compact human duration such as "2h 5m" or "45s".
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

// Render builds the plain-text alert email.
func Render(a Alert) email.Message {
	name, url := clean(a.MonitorName), clean(a.MonitorURL)
	started := a.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
	link := a.AppBaseURL + "/incidents"
	footer := "\n\nView incidents: " + link + "\nManage alert recipients: " + a.AppBaseURL + "/notifications\n"

	m := email.Message{From: a.From, To: a.To}
	switch a.Event {
	case EventRecovery:
		m.Subject = "[HawkEye] RECOVERED: " + name
		dur := ""
		if a.ResolvedAt != nil {
			dur = fmt.Sprintf("\nOutage lasted: %s (ended %s)", Duration(a.ResolvedAt.Sub(a.StartedAt)), a.ResolvedAt.UTC().Format("2006-01-02 15:04 UTC"))
		}
		m.Text = fmt.Sprintf("%s is back up.\n\nURL: %s\nOutage started: %s%s", name, url, started, dur) + footer
	default:
		m.Subject = "[HawkEye] DOWN: " + name
		cause := ""
		if a.Cause != "" {
			cause = "\nCause: " + clean(a.Cause)
		}
		m.Text = fmt.Sprintf("%s is down.\n\nURL: %s\nStarted: %s%s\n\nWe confirmed this with consecutive failed checks, "+
			"so it is unlikely to be a one-off blip. We will email you when it recovers.", name, url, started, cause) + footer
	}
	return m
}
