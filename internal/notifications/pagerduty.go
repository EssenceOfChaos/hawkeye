package notifications

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"hawkeye/internal/validation"
)

// PagerDuty, through its Events API v2: an outage triggers a PagerDuty alert and its recovery resolves the same alert (they share a dedup key built
// from the monitor and the incident's start), so the on-call person is paged once and the incident closes itself. Slowdowns work the same way.
// Certificate and domain expiry warnings trigger an alert that the team resolves in PagerDuty after renewing; later warnings for the same expiry
// date update that alert rather than open another.
//
// The customer enters the integration key of an "Events API v2" integration on one of their PagerDuty services, and the service region (US or EU
// accounts use different hosts). The stored destination is the sealed enqueue URL with the key in its query, moved into the body when sending.

var pagerDutyKey = regexp.MustCompile(`^[A-Za-z0-9]{32}$`)

// PagerDuty service regions and their Events API hosts.
var pagerDutyHosts = map[string]string{"us": "events.pagerduty.com", "eu": "events.eu.pagerduty.com"}

// PagerDutyURL validates an integration key and region ("us", the default, or "eu") and returns the enqueue URL that carries the key, with a label
// that shows only the region and the key's last four characters.
func PagerDutyURL(key, region string) (*url.URL, string, error) {
	key, region = strings.TrimSpace(key), strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		region = "us"
	}
	host, ok := pagerDutyHosts[region]
	switch {
	case key == "":
		return nil, "", validation.New("destination", "is required")
	case !pagerDutyKey.MatchString(key):
		return nil, "", validation.New("destination", "must be the 32-character integration key of an Events API v2 integration in PagerDuty")
	case !ok:
		return nil, "", validation.New("region", "must be us or eu")
	}
	u := &url.URL{Scheme: "https", Host: host, Path: "/v2/enqueue", RawQuery: url.Values{"routing_key": {key}}.Encode()}
	return u, strings.ToUpper(region) + " service, key ending " + key[len(key)-4:], nil
}

// pagerDutyEvent renders an alert as an Events API v2 event for the given integration key.
func pagerDutyEvent(key string, a Alert, m chatMessage) map[string]any {
	dedup, action, severity := pagerDutyKeys(a)
	ev := map[string]any{"routing_key": key, "event_action": action, "dedup_key": dedup}
	if action == "resolve" {
		return ev // a resolve carries nothing else; PagerDuty closes the alert with this dedup key
	}
	source := clean(a.MonitorName)
	if a.MonitorURL != "" {
		if u, err := url.Parse(a.MonitorURL); err == nil && u.Hostname() != "" {
			source = u.Hostname()
		}
	}
	if a.Event == EventDomainExpiry {
		source = clean(a.DomainName)
	}
	if source == "" {
		source = "HawkEye"
	}
	details := map[string]any{}
	if m.Target != "" {
		details["monitored"] = m.Target
	}
	if len(m.Lines) > 0 {
		details["details"] = strings.Join(m.Lines, "\n")
	}
	payload := map[string]any{"summary": truncateRunes(plainTitle(m.Title), 1024), "source": truncateRunes(source, 255), "severity": severity,
		"class": a.Event, "custom_details": details}
	if a.MonitorName != "" {
		payload["component"] = truncateRunes(clean(a.MonitorName), 255)
	}
	if !a.StartedAt.IsZero() && a.Event != EventTest {
		payload["timestamp"] = a.StartedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	ev["payload"] = payload
	ev["client"], ev["client_url"] = "HawkEye", m.Link
	ev["links"] = []any{map[string]any{"href": m.Link, "text": "Open in HawkEye"}}
	return ev
}

// pagerDutyKeys decides how an alert maps onto PagerDuty: the dedup key that ties a trigger to its resolve, the action and the severity.
func pagerDutyKeys(a Alert) (dedup, action, severity string) {
	unix := func(t interface{ Unix() int64 }) string { return strconv.FormatInt(t.Unix(), 10) }
	switch a.Event {
	case EventTest:
		return "hawkeye:test:" + a.TestID, "trigger", "info"
	case EventRecovery:
		return "hawkeye:incident:" + a.MonitorID + ":" + unix(a.StartedAt), "resolve", ""
	case EventSlow:
		return "hawkeye:slowdown:" + a.MonitorID + ":" + unix(a.StartedAt), "trigger", "warning"
	case EventSlowRecovery:
		return "hawkeye:slowdown:" + a.MonitorID + ":" + unix(a.StartedAt), "resolve", ""
	case EventSSLExpiry:
		sev := "warning"
		if a.SSLBucket == 0 {
			sev = "error"
		}
		return "hawkeye:ssl:" + a.MonitorID + ":" + unix(a.SSLExpiresAt), "trigger", sev
	case EventDomainExpiry:
		sev := "warning"
		if a.DomainBucket == 0 {
			sev = "error"
		}
		return "hawkeye:domain:" + strings.ToLower(a.DomainName) + ":" + unix(a.DomainExpiresAt), "trigger", sev
	}
	return "hawkeye:incident:" + a.MonitorID + ":" + unix(a.StartedAt), "trigger", "critical" // an outage
}

// plainTitle drops the leading emoji the chat channels show, which an on-call tool's summary doesn't need.
func plainTitle(s string) string {
	r := []rune(s)
	if len(r) > 2 && !unicode.IsLetter(r[0]) && !unicode.IsDigit(r[0]) {
		if i := strings.IndexRune(s, ' '); i > 0 {
			return strings.TrimSpace(s[i+1:])
		}
	}
	return s
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
