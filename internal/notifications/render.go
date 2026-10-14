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

// Render builds the alert email (branded HTML plus a plain-text alternative).
func Render(a Alert) email.Message {
	name, url := clean(a.MonitorName), clean(a.MonitorURL)
	started := a.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
	c := email.Content{
		ButtonLabel: "View incidents", ButtonURL: a.AppBaseURL + "/incidents", AppBaseURL: a.AppBaseURL,
		Note: "Manage who receives alerts: " + a.AppBaseURL + "/notifications",
	}
	var subject string
	switch a.Event {
	case EventRecovery:
		subject = "HawkEye recovery: " + name + " is responding again"
		c.Tone = email.ToneSuccess
		c.Preheader = name + " is responding again."
		c.Heading = name + " is responding again"
		c.Paragraphs = []string{"Good news. HawkEye has recorded enough successful checks to resolve the incident affecting " + name + "."}
		c.Facts = []email.Fact{{Label: "Website", Value: url}, {Label: "Incident began", Value: started}}
		if a.ResolvedAt != nil {
			c.Facts = append(c.Facts,
				email.Fact{Label: "Recovery confirmed", Value: a.ResolvedAt.UTC().Format("2006-01-02 15:04 UTC")},
				email.Fact{Label: "Incident duration", Value: Duration(a.ResolvedAt.Sub(a.StartedAt))})
		}
		c.Paragraphs = append(c.Paragraphs, "Review the incident timeline to see the recorded failures and recovery checks.")
	default:
		subject = "HawkEye alert: " + name + " may be down"
		c.Tone = email.ToneAlert
		c.Preheader = "HawkEye detected repeated failures while checking " + name + "."
		c.Heading = name + " may be down"
		c.Paragraphs = []string{
			"HawkEye has detected repeated failures while checking " + name + ".",
			"We recommend reviewing the website and its hosting environment. HawkEye will continue monitoring according to your configured check interval, and will email you once when the website recovers.",
		}
		c.Facts = []email.Fact{{Label: "Website", Value: url}, {Label: "Detected at", Value: started}}
		if a.Cause != "" {
			c.Facts = append(c.Facts, email.Fact{Label: "Latest result", Value: clean(a.Cause)})
		}
		c.Facts = append(c.Facts, email.Fact{Label: "Incident status", Value: "Open"})
	}
	return c.Message(a.From, a.To, subject)
}
