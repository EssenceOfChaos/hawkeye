package notifications

import (
	"fmt"
	"strconv"
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
	MonitorType string // http, heartbeat or dns; empty is treated as http
	Cause       string
	StartedAt   time.Time
	ResolvedAt  *time.Time
	AppBaseURL  string
	From        string
	To          string

	// A test alert (EventTest, email only): who asked for it, and when it was sent (zero means now).
	RequestedBy string
	SentAt      time.Time
	// TestID ties a PagerDuty test trigger to the resolve that follows it, so the test closes itself.
	TestID string

	// Certificate warnings (EventSSLExpiry): when the certificate expires and which warning bucket (30, 14, 7, 1 days, 0 = expired) this is.
	SSLExpiresAt time.Time
	SSLBucket    int

	// Domain warnings (EventDomainExpiry): the domain, when its registration expires and which warning bucket this is (as for certificates).
	DomainName      string
	DomainExpiresAt time.Time
	DomainBucket    int

	// Slow-response alerts (EventSlow, EventSlowRecovery): the threshold and the slowest response seen. StartedAt and ResolvedAt are the slowdown's.
	SlowThresholdMs int
	SlowPeakMs      int
	SlowCause       string // what got slower, compared with the past week; empty when it can't be said

	// DNS change alerts (EventDNSChange, EventDNSChangeClosed): the record type, its answers before and after, how the change closed (accepted or
	// reverted; "open" for the first alert) and who accepted it. StartedAt is when it was detected, ResolvedAt when it closed.
	DNSRecordType string
	DNSBefore     []string
	DNSAfter      []string
	DNSStatus     string
	DNSAcceptedBy string

	// Flapping (EventFlapping, EventFlappingEnded, and the outage and recovery of an incident that began while its monitor was flapping): the
	// episode, when it began, how many outages it counted, and its status (open while it lasts; then settled, stayed_down or ended). For the
	// flapping events StartedAt and ResolvedAt are the episode's, and Cause is that of its latest outage.
	FlapEpisodeID string
	FlappingSince *time.Time
	FlapOutages   int
	FlapStatus    string

	// Outage alerts of website and API monitors: what the confirming check saw (nil without a snapshot). Never the body excerpt.
	Snapshot *AlertSnapshot
	// Recovery alerts: the AI analysis of the outage's likely cause, when the organization turned analysis on and one was written.
	Analysis *AlertAnalysis

	// Group holds the alerts combined into this message (see Group): two or more of one event, to one destination. Empty for a single alert.
	Group []Alert
}

// FlapSettle is how long a flapping monitor stays up, with no new outage, before it counts as settled (internal/incidents/flap.go).
const FlapSettle = 30 * time.Minute

// IsHeartbeat reports whether the alert is about a heartbeat (cron job) monitor, which has no website to name.
func (a Alert) IsHeartbeat() bool { return a.MonitorType == "heartbeat" }

// IsDNS reports whether the alert is about a DNS monitor: its host name may answer perfectly well, just not with the expected record.
func (a Alert) IsDNS() bool { return a.MonitorType == "dns" }

func (a Alert) monitorType() string {
	switch {
	case a.IsHeartbeat():
		return "heartbeat"
	case a.IsDNS():
		return "dns"
	}
	return "http"
}

// heartbeatProblem completes "<job> …" for an outage alert from the result that opened it: a missed ping or a failure the job reported itself.
func heartbeatProblem(cause string) string {
	if strings.HasPrefix(cause, "The job ") {
		return "reported a failure"
	}
	return "did not check in on time"
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
	if len(a.Group) > 0 {
		return renderGroup(a)
	}
	if a.Event == EventFlapping || a.Event == EventFlappingEnded {
		return renderFlap(a, name, url)
	}
	if a.Event == EventSSLExpiry {
		return renderSSL(a, name, url)
	}
	if a.Event == EventDomainExpiry {
		return renderDomain(a)
	}
	if a.Event == EventTest {
		return renderTest(a)
	}
	if a.Event == EventSlow || a.Event == EventSlowRecovery {
		return renderSlow(a, name, url)
	}
	if a.Event == EventDNSChange || a.Event == EventDNSChangeClosed {
		return renderDNSChange(a, url)
	}
	if a.IsHeartbeat() {
		return renderHeartbeat(a, name)
	}
	if a.IsDNS() {
		return renderDNS(a, name, url)
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
		if an := a.Analysis; an != nil {
			c.Facts = append(c.Facts, email.Fact{Label: "Likely cause (AI, may be wrong)", Value: clean(an.LikelyCause)})
			if an.NextStep != "" {
				c.Facts = append(c.Facts, email.Fact{Label: "Suggested next step", Value: clean(an.NextStep)})
			}
		}
		c.Paragraphs = append(c.Paragraphs, "Review the incident timeline to see the recorded failures and recovery checks.")
	default:
		subject = "HawkEye alert: " + name + " may be offline"
		c.Tone, c.Picture = email.ToneAlert, email.PictureMonitorDown
		c.Preheader = "HawkEye detected repeated failures while checking " + name + "."
		c.Heading = name + " may be offline"
		c.Paragraphs = []string{
			"HawkEye has detected repeated failures while checking " + name + ".",
			"We recommend reviewing the website and its hosting environment. HawkEye will continue monitoring according to your configured check interval, and will email you once when the website recovers.",
		}
		c.Paragraphs = withFlapNote(c.Paragraphs, a, name)
		c.Facts = []email.Fact{{Label: "Website", Value: url}, {Label: "Detected at", Value: started}}
		if a.Cause != "" {
			c.Facts = append(c.Facts, email.Fact{Label: "Latest result", Value: clean(a.Cause)})
		}
		if a.Snapshot != nil {
			c.Facts = append(c.Facts, snapshotFacts(a.Snapshot)...)
			c.Quote = snapshotQuote(a.Snapshot)
		}
		c.Facts = append(c.Facts, email.Fact{Label: "Incident status", Value: "Open"})
	}
	return c.Message(a.From, a.To, subject)
}

// renderDNS is the outage and recovery alert of a DNS monitor: a record stopped resolving, or stopped matching the expected value.
func renderDNS(a Alert, name, host string) email.Message {
	started := a.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
	c := email.Content{
		ButtonLabel: "View the DNS check", ButtonURL: a.AppBaseURL + "/monitors/" + a.MonitorID, AppBaseURL: a.AppBaseURL,
		Note: "Manage who receives alerts: " + a.AppBaseURL + "/notifications",
	}
	if a.Event == EventRecovery {
		c.Tone = email.ToneSuccess
		c.Heading = "DNS for " + name + " is resolving correctly again"
		c.Preheader = c.Heading + "."
		c.Paragraphs = []string{"Good news. HawkEye has recorded enough successful lookups to resolve the incident affecting " + name + "."}
		c.Facts = []email.Fact{{Label: "Host name", Value: host}, {Label: "Problem began", Value: started}}
		if a.ResolvedAt != nil {
			c.Facts = append(c.Facts,
				email.Fact{Label: "Recovery confirmed", Value: a.ResolvedAt.UTC().Format("2006-01-02 15:04 UTC")},
				email.Fact{Label: "Incident duration", Value: Duration(a.ResolvedAt.Sub(a.StartedAt))})
		}
		return c.Message(a.From, a.To, "HawkEye recovery: DNS for "+name+" is resolving correctly again")
	}
	c.Tone, c.Picture = email.ToneAlert, email.PictureMonitorDown
	c.Heading = "DNS check failing for " + name
	c.Preheader = "HawkEye detected repeated failures while looking up " + name + "."
	c.Paragraphs = []string{
		"HawkEye has detected repeated failures while looking up " + name + ".",
		"We recommend reviewing the record with your DNS provider and confirming the change was intended. HawkEye will keep looking it up on your configured schedule, and will email you once when it recovers.",
	}
	c.Paragraphs = withFlapNote(c.Paragraphs, a, name)
	c.Facts = []email.Fact{{Label: "Host name", Value: host}, {Label: "Detected at", Value: started}}
	if a.Cause != "" {
		c.Facts = append(c.Facts, email.Fact{Label: "Latest result", Value: clean(a.Cause)})
	}
	c.Facts = append(c.Facts, email.Fact{Label: "Incident status", Value: "Open"})
	return c.Message(a.From, a.To, "HawkEye alert: DNS check failing for "+name)
}

// renderHeartbeat is the outage and recovery alert of a heartbeat (cron job) monitor: the job stopped checking in, or said it failed.
func renderHeartbeat(a Alert, name string) email.Message {
	started := a.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
	c := email.Content{
		ButtonLabel: "View the ping log", ButtonURL: a.AppBaseURL + "/monitors/" + a.MonitorID, AppBaseURL: a.AppBaseURL,
		Note: "Manage who receives alerts: " + a.AppBaseURL + "/notifications",
	}
	var subject string
	if a.Event == EventRecovery {
		subject = "HawkEye recovery: " + name + " is checking in again"
		c.Tone = email.ToneSuccess
		c.Preheader = name + " is checking in again."
		c.Heading = name + " is checking in again"
		c.Paragraphs = []string{"Good news. HawkEye has received a successful ping from " + name + ", so the incident is resolved."}
		c.Facts = []email.Fact{{Label: "Problem began", Value: started}}
		if a.ResolvedAt != nil {
			c.Facts = append(c.Facts,
				email.Fact{Label: "Successful ping", Value: a.ResolvedAt.UTC().Format("2006-01-02 15:04 UTC")},
				email.Fact{Label: "Incident duration", Value: Duration(a.ResolvedAt.Sub(a.StartedAt))})
		}
		return c.Message(a.From, a.To, subject)
	}
	problem := heartbeatProblem(a.Cause)
	subject = "HawkEye alert: " + name + " " + problem
	c.Tone, c.Picture = email.ToneAlert, email.PictureMonitorDown
	c.Heading = name + " " + problem
	c.Preheader = c.Heading + "."
	if problem == "reported a failure" {
		c.Paragraphs = []string{"The scheduled job " + name + " told HawkEye that its last run failed."}
	} else {
		c.Paragraphs = []string{"HawkEye expected a ping from the scheduled job " + name + " and none arrived in time, including the grace time. The job may not have run, may have failed before pinging, or may be unable to reach HawkEye."}
	}
	c.Paragraphs = withFlapNote(c.Paragraphs, a, name)
	c.Paragraphs = append(c.Paragraphs, "Check the job's logs and the ping log in HawkEye. You will get one more message when the job pings successfully again.")
	c.Facts = []email.Fact{{Label: "Detected at", Value: started}}
	if a.Cause != "" {
		c.Facts = append(c.Facts, email.Fact{Label: "Latest result", Value: clean(a.Cause)})
	}
	c.Facts = append(c.Facts, email.Fact{Label: "Incident status", Value: "Open"})
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
		Picture:   email.PictureTestAlert,
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

// Milliseconds renders a response time such as "2,450 ms".
func Milliseconds(ms int) string {
	s := strconv.Itoa(ms)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s + " ms"
}

// renderSlow is the slow-response alert and its "back to normal" follow-up. Slowness is a warning, not an outage, so the alert is in the brand
// color rather than red, and it says plainly that the website is still online.
func renderSlow(a Alert, name, url string) email.Message {
	threshold, since := Milliseconds(a.SlowThresholdMs), a.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
	c := email.Content{
		ButtonLabel: "View response times", ButtonURL: a.AppBaseURL + "/monitors/" + a.MonitorID, AppBaseURL: a.AppBaseURL,
		Note:  "Change the threshold or turn these alerts off in the monitor's settings. Manage who receives alerts: " + a.AppBaseURL + "/notifications",
		Facts: []email.Fact{{Label: "Website", Value: url}, {Label: "Threshold", Value: threshold}},
	}
	var subject string
	if a.Event == EventSlowRecovery {
		subject = "HawkEye: " + name + " is responding normally again"
		c.Tone = email.ToneSuccess
		c.Preheader = name + " is back under " + threshold + "."
		c.Heading = name + " is responding normally again"
		c.Paragraphs = []string{"Response times for " + name + " are back at or under your threshold of " + threshold + "."}
		c.Facts = append(c.Facts, email.Fact{Label: "Slow since", Value: since}, email.Fact{Label: "Slowest response", Value: Milliseconds(a.SlowPeakMs)})
		if a.ResolvedAt != nil {
			c.Facts = append(c.Facts, email.Fact{Label: "Slow for", Value: Duration(a.ResolvedAt.Sub(a.StartedAt))})
		}
		return c.Message(a.From, a.To, subject)
	}
	subject = "HawkEye alert: " + name + " is responding slowly"
	c.Preheader = name + " is online but slower than " + threshold + "."
	c.Heading = name + " is responding slowly"
	c.Paragraphs = []string{
		"The website is online, but its responses have stayed above your threshold of " + threshold + " for several checks in a row.",
	}
	if a.SlowCause != "" {
		c.Paragraphs = append(c.Paragraphs, a.SlowCause)
	}
	c.Paragraphs = append(c.Paragraphs, "Slow responses are often an early sign of trouble: a busy server, a slow database or a problem with the hosting. HawkEye keeps checking and will tell you once when response times are back to normal.")
	c.Facts = append(c.Facts, email.Fact{Label: "Slowest response", Value: Milliseconds(a.SlowPeakMs)}, email.Fact{Label: "Confirmed at", Value: since})
	return c.Message(a.From, a.To, subject)
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

func renderDomain(a Alert) email.Message {
	now := time.Now()
	name := clean(a.DomainName)
	expires := a.DomainExpiresAt.UTC().Format("2006-01-02 15:04 UTC")
	c := email.Content{
		ButtonLabel: "View domains", ButtonURL: a.AppBaseURL + "/domains", AppBaseURL: a.AppBaseURL,
		Note:  "Manage who receives alerts: " + a.AppBaseURL + "/notifications",
		Facts: []email.Fact{{Label: "Domain", Value: name}, {Label: "Registration expires", Value: expires}},
	}
	var subject string
	if a.DomainBucket == 0 {
		subject = "HawkEye alert: the domain " + name + " has expired"
		c.Tone = email.ToneAlert
		c.Preheader = "The registration of " + name + " has expired."
		c.Heading = "Domain expired: " + name
		c.Paragraphs = []string{
			"The registration of " + name + " has expired. Until it is renewed the website and any email on this domain may stop working, and after a grace period the registrar may release the name to others.",
			"Renew it with your registrar as soon as possible. Some registrars charge a redemption fee once the grace period has begun.",
		}
		c.Facts = append(c.Facts, email.Fact{Label: "Status", Value: "Expired"})
	} else {
		left := sslLeft(a.DomainExpiresAt, now)
		subject = "HawkEye reminder: the domain " + name + " expires in " + left
		if left == "less than a day" {
			subject = "HawkEye reminder: the domain " + name + " expires within a day"
		}
		if a.DomainBucket <= 7 {
			c.Tone = email.ToneAlert
		}
		c.Preheader = "The registration of " + name + " expires in " + left + "."
		c.Heading = "Domain expiring soon: " + name
		c.Paragraphs = []string{
			"The registration of " + name + " is approaching its expiration date. Renew it with your registrar before then, or the website and any email on this domain may stop working.",
			"If the domain renews automatically, check that the payment method on file is still valid.",
		}
		if a.DomainBucket > 1 {
			c.Paragraphs = append(c.Paragraphs, "HawkEye will remind you again as the date approaches.")
		}
		c.Facts = append(c.Facts, email.Fact{Label: "Time remaining", Value: left})
	}
	return c.Message(a.From, a.To, subject)
}

// answerList is a set of DNS answers as one line of text.
func answerList(answers []string) string {
	if len(answers) == 0 {
		return "(none)"
	}
	out := make([]string, len(answers))
	for i, a := range answers {
		out[i] = clean(a)
	}
	return strings.Join(out, ", ")
}

// renderDNSChange is the DNS change alert and its follow-up (accepted, or changed back). A change is a warning, not an outage: the site may be
// working perfectly, and the change may be intended.
func renderDNSChange(a Alert, host string) email.Message {
	record := a.DNSRecordType + " record of " + host
	c := email.Content{
		ButtonLabel: "Review the change", ButtonURL: a.AppBaseURL + "/monitors/" + a.MonitorID, AppBaseURL: a.AppBaseURL,
		Note:  "Turn DNS change alerts off in the monitor's settings. Manage who receives alerts: " + a.AppBaseURL + "/notifications",
		Facts: []email.Fact{{Label: "Record", Value: record}, {Label: "Before", Value: answerList(a.DNSBefore)}, {Label: "After", Value: answerList(a.DNSAfter)}},
	}
	if a.Event == EventDNSChangeClosed {
		c.Tone = email.ToneSuccess
		if a.DNSStatus == "accepted" {
			who := "Someone on your team"
			if a.DNSAcceptedBy != "" {
				who = clean(a.DNSAcceptedBy)
			}
			subject := "HawkEye: the DNS change for " + host + " was accepted"
			c.Preheader, c.Heading = who+" marked the change as intended.", "The DNS change for "+host+" was accepted"
			c.Paragraphs = []string{who + " marked the change to the " + record + " as intended. Its new answers are now the ones HawkEye expects."}
			return c.Message(a.From, a.To, subject)
		}
		subject := "HawkEye: the DNS record for " + host + " changed back"
		c.Preheader, c.Heading = "The "+record+" is back to what it was.", "The DNS record for "+host+" changed back"
		c.Paragraphs = []string{"The " + record + " is answering as it did before the change, so the change is closed."}
		return c.Message(a.From, a.To, subject)
	}
	subject := "HawkEye alert: DNS record changed for " + host
	c.Preheader = "The " + record + " changed. Check that it was intended."
	c.Heading = "DNS record changed for " + host
	c.Paragraphs = []string{
		"The " + record + " has answered differently in several checks in a row. The website may well be working: this is about what changed.",
		"If you or your team made this change, open the monitor and mark it as intended. If not, check your domain registrar and DNS provider " +
			"accounts now: an unexpected change of name servers or mail servers is how domains are hijacked and email goes astray.",
	}
	return c.Message(a.From, a.To, subject)
}
