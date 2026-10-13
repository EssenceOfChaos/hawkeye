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
		m.Subject = "HawkEye recovery: " + name + " is responding again"
		dur := ""
		if a.ResolvedAt != nil {
			dur = fmt.Sprintf("\nRecovery confirmed: %s\nIncident duration: %s", a.ResolvedAt.UTC().Format("2006-01-02 15:04 UTC"), Duration(a.ResolvedAt.Sub(a.StartedAt)))
		}
		m.Text = fmt.Sprintf("Good news. HawkEye has recorded enough successful checks to resolve the incident affecting %s.\n\nWebsite: %s\nIncident began: %s%s\n\n"+
			"Review the incident timeline to see the recorded failures and recovery checks.", name, url, started, dur) + footer
	default:
		m.Subject = "HawkEye alert: " + name + " may be down"
		cause := ""
		if a.Cause != "" {
			cause = "\nLatest result: " + clean(a.Cause)
		}
		m.Text = fmt.Sprintf("HawkEye has detected repeated failures while checking %s.\n\nWebsite: %s\nDetected at: %s%s\nIncident status: Open\n\n"+
			"We recommend reviewing the website and its hosting environment. HawkEye will continue monitoring according to your configured check interval, "+
			"and will email you once when the website recovers.", name, url, started, cause) + footer
	}
	return m
}
