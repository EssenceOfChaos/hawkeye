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
	MonitorID   string // for links to the monitor page
	MonitorName string
	MonitorURL  string
	Cause       string
	StartedAt   time.Time
	ResolvedAt  *time.Time
	AppBaseURL  string
	From        string
	To          string

	// A test alert (EventTest, email only): who asked for it, and when it was sent (zero means now).
	RequestedBy string
	SentAt      time.Time

	// Certificate warnings (EventSSLExpiry): when the certificate expires and which warning bucket (30, 14, 7, 1 days, 0 = expired) this is.
	SSLExpiresAt time.Time
	SSLBucket    int
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
	if a.Event == EventSSLExpiry {
		return renderSSL(a, name, url)
	}
	if a.Event == EventTest {
		return renderTest(a)
	}
	started := a.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
	c := email.Content{
		ButtonLabel: "View incidents", ButtonURL: a.AppBaseURL + "/incidents", AppBaseURL: a.AppBaseURL,
		Note: "Manage who receives alerts: " + a.AppBaseURL + "/notifications",
	}
	var subject string
	switch a.Event {
	case EventRecovery:
		subject = "HawkEye recovery: " + name + " is back online"
		c.Tone = email.ToneSuccess
		c.Preheader = name + " is back online."
		c.Heading = name + " is back online"
		c.Paragraphs = []string{"Good news. HawkEye has recorded enough successful checks to resolve the incident affecting " + name + "."}
		c.Facts = []email.Fact{{Label: "Website", Value: url}, {Label: "Incident began", Value: started}}
		if a.ResolvedAt != nil {
			c.Facts = append(c.Facts,
				email.Fact{Label: "Recovery confirmed", Value: a.ResolvedAt.UTC().Format("2006-01-02 15:04 UTC")},
				email.Fact{Label: "Incident duration", Value: Duration(a.ResolvedAt.Sub(a.StartedAt))})
		}
		c.Paragraphs = append(c.Paragraphs, "Review the incident timeline to see the recorded failures and recovery checks.")
	default:
		subject = "HawkEye alert: " + name + " may be offline"
		c.Tone = email.ToneAlert
		c.Preheader = "HawkEye detected repeated failures while checking " + name + "."
		c.Heading = name + " may be offline"
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

// renderTest is the email sent by "Send test" on the alert recipients page. It must be unmistakably a test, never something that looks like an outage:
// no monitor, no red, and it says outright that nothing is wrong.
func renderTest(a Alert) email.Message {
	sent := a.SentAt
	if sent.IsZero() {
		sent = time.Now()
	}
	c := email.Content{
		Preheader: "This is only a test. Nothing is wrong with your websites.",
		Heading:   "Your alerts are working",
		Paragraphs: []string{
			"This is a test alert from HawkEye. If you can read this, real alerts will reach this address.",
			"Nothing is wrong: no website is offline and nothing needs your attention.",
		},
		Facts:       []email.Fact{{Label: "Sent to", Value: clean(a.To)}},
		ButtonLabel: "Manage alert recipients", ButtonURL: a.AppBaseURL + "/notifications", AppBaseURL: a.AppBaseURL,
		Note: "A real alert looks like this too: red when a website may be offline, green when it is back online.",
	}
	if a.RequestedBy != "" {
		c.Facts = append(c.Facts, email.Fact{Label: "Requested by", Value: clean(a.RequestedBy)})
	}
	c.Facts = append(c.Facts, email.Fact{Label: "Sent at", Value: sent.UTC().Format("2006-01-02 15:04 UTC")})
	return c.Message(a.From, a.To, "HawkEye test alert")
}

// sslLeft describes the time until expiry in words ("12 days", "1 day", "less than a day").
func sslLeft(expires, now time.Time) string {
	d := expires.Sub(now)
	switch days := int(d / (24 * time.Hour)); {
	case d <= 0:
		return "expired"
	case days >= 2:
		return fmt.Sprintf("%d days", days)
	case days == 1:
		return "1 day"
	}
	return "less than a day"
}

func renderSSL(a Alert, name, url string) email.Message {
	now := time.Now()
	expires := a.SSLExpiresAt.UTC().Format("2006-01-02 15:04 UTC")
	c := email.Content{
		ButtonLabel: "View monitor", ButtonURL: a.AppBaseURL + "/monitors/" + a.MonitorID, AppBaseURL: a.AppBaseURL,
		Note:  "Manage who receives alerts: " + a.AppBaseURL + "/notifications",
		Facts: []email.Fact{{Label: "Website", Value: url}, {Label: "Certificate expires", Value: expires}},
	}
	var subject string
	if a.SSLBucket == 0 {
		subject = "HawkEye alert: SSL certificate for " + name + " has expired"
		c.Tone = email.ToneAlert
		c.Preheader = "The SSL certificate for " + name + " has expired."
		c.Heading = "SSL certificate expired for " + name
		c.Paragraphs = []string{
			"The SSL certificate for " + name + " has expired. Browsers will warn visitors that the connection is not secure, and some will refuse to load the website.",
			"Renew and install the certificate as soon as possible. HawkEye will pick up the new certificate automatically on its next check.",
		}
		c.Facts = append(c.Facts, email.Fact{Label: "Status", Value: "Expired"})
	} else {
		left := sslLeft(a.SSLExpiresAt, now)
		subject = "HawkEye reminder: SSL certificate for " + name + " expires in " + left
		if left == "less than a day" {
			subject = "HawkEye reminder: SSL certificate for " + name + " expires within a day"
		}
		if a.SSLBucket <= 7 {
			c.Tone = email.ToneAlert
		}
		c.Preheader = "The SSL certificate for " + name + " expires in " + left + "."
		c.Heading = "SSL certificate expiring soon for " + name
		c.Paragraphs = []string{
			"The SSL certificate for " + name + " is approaching its expiration date. Renew it before then so visitors do not see security warnings.",
		}
		if a.SSLBucket > 1 {
			c.Paragraphs = append(c.Paragraphs, "HawkEye will remind you again as the date approaches.")
		}
		c.Facts = append(c.Facts, email.Fact{Label: "Time remaining", Value: left})
	}
	return c.Message(a.From, a.To, subject)
}
