package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hawkeye/internal/checks"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/validation"
)

// Channels.
const (
	ChannelEmail     = "email"
	ChannelWebhook   = "webhook"
	ChannelSlack     = "slack"
	ChannelDiscord   = "discord"
	ChannelTeams     = "teams"
	ChannelTelegram  = "telegram"
	ChannelPagerDuty = "pagerduty"
)

// EventTest is sent once when a webhook or chat destination is added, to prove it works.
const EventTest = "test"

// errPermanent matches (via errors.Is) a delivery failure that retrying cannot fix, such as the receiver rejecting the request.
var errPermanent = errors.New("permanent delivery failure")

type permanentError struct{ msg string }

func (e permanentError) Error() string        { return e.msg }
func (e permanentError) Is(target error) bool { return target == errPermanent }

func permanent(format string, args ...any) error { return permanentError{fmt.Sprintf(format, args...)} }

const (
	maxURLLength     = 2048
	postTimeout      = 10 * time.Second
	signatureHeader  = "X-HawkEye-Signature"
	timestampHeader  = "X-HawkEye-Timestamp"
	eventHeader      = "X-HawkEye-Event"
	deliveryHeader   = "X-HawkEye-Delivery"
	signingKeyPrefix = "whsec_"
)

// NewHTTPClient returns the client used to reach webhook and chat URLs: the same SSRF-safe client the monitors use,
// because these URLs are chosen by customers. Tests pass their own.
func NewHTTPClient() *http.Client { return safehttp.New(safehttp.Config{}, postTimeout) }

// IsURLChannel reports whether a channel delivers by HTTPS request (everything but email).
func IsURLChannel(channel string) bool {
	switch channel {
	case ChannelWebhook, ChannelSlack, ChannelDiscord, ChannelTeams, ChannelTelegram, ChannelPagerDuty:
		return true
	}
	return false
}

var (
	discordPath      = regexp.MustCompile(`^/api(/v\d+)?/webhooks/\d+/[A-Za-z0-9_-]+/?$`)
	telegramBotToken = regexp.MustCompile(`^\d{3,20}:[A-Za-z0-9_-]{20,100}$`)
	telegramChatID   = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{3,31})$`)
)

// ParseDestination validates what the customer entered for a URL channel and returns the URL to store (sealed) with a label that
// is safe to display. Most channels take a URL. extra is what a channel needs besides its destination: for Telegram, destination is the chat ID
// and extra the bot's token; for PagerDuty, destination is the integration key and extra the service region (us or eu).
func ParseDestination(channel, destination, extra string) (*url.URL, string, error) {
	switch channel {
	case ChannelTelegram:
		return TelegramURL(extra, destination)
	case ChannelPagerDuty:
		return PagerDutyURL(destination, extra)
	}
	return ParseURL(channel, destination)
}

// ParseURL validates a webhook, Slack, Discord or Teams URL and returns it with a masked label that is safe to display
// (the path or query of these URLs is a credential).
func ParseURL(channel, raw string) (u *url.URL, label string, err error) {
	raw = strings.TrimSpace(raw)
	bad := func(msg string) (*url.URL, string, error) { return nil, "", validation.New("destination", msg) }
	if raw == "" {
		return bad("is required")
	}
	if len(raw) > maxURLLength {
		return bad("is too long")
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" || u.Hostname() == "" {
		return bad("must be a valid URL")
	}
	if u.Scheme != "https" {
		return bad("must start with https://")
	}
	if u.User != nil {
		return bad("must not contain a username or password")
	}
	if p := u.Port(); p != "" && p != "443" {
		return bad("must use port 443")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return bad("must use a host name, not an IP address")
	}
	host := strings.ToLower(u.Hostname())
	switch channel {
	case ChannelSlack:
		if host != "hooks.slack.com" || !strings.HasPrefix(u.Path, "/services/") {
			return bad("must be a Slack incoming webhook URL (https://hooks.slack.com/services/…)")
		}
	case ChannelDiscord:
		switch host {
		case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
		default:
			return bad("must be a Discord webhook URL (https://discord.com/api/webhooks/…)")
		}
		if !discordPath.MatchString(u.Path) {
			return bad("must be a Discord webhook URL (https://discord.com/api/webhooks/…)")
		}
	case ChannelTeams:
		// Teams takes incoming alerts through a Workflows (Power Automate) webhook; the older Office 365 connector URLs are retired.
		logicApp := strings.HasSuffix(host, ".logic.azure.com") && strings.HasPrefix(u.Path, "/workflows/")
		powerPlatform := strings.HasSuffix(host, ".powerplatform.com") && strings.Contains(u.Path, "/workflows/")
		if !logicApp && !powerPlatform {
			return bad("must be a Teams Workflows webhook URL (…logic.azure.com/workflows/… or …powerplatform.com/…/workflows/…)")
		}
	}
	return u, host + "/…", nil
}

// TelegramURL validates a bot token and chat ID and returns the Bot API URL that sends to that chat (the token in its path is
// the credential; the chat ID travels in the query and is moved into the body at send time). The label shows only the chat.
func TelegramURL(botToken, chatID string) (*url.URL, string, error) {
	botToken, chatID = strings.TrimSpace(botToken), strings.TrimSpace(chatID)
	switch {
	case botToken == "":
		return nil, "", validation.New("bot_token", "is required")
	case !telegramBotToken.MatchString(botToken):
		return nil, "", validation.New("bot_token", "must be a bot token from @BotFather (like 123456789:AAE…)")
	case chatID == "":
		return nil, "", validation.New("destination", "is required")
	case !telegramChatID.MatchString(chatID):
		return nil, "", validation.New("destination", "must be a numeric chat ID (like -1001234567890) or a public @channelname")
	}
	u := &url.URL{Scheme: "https", Host: "api.telegram.org", Path: "/bot" + botToken + "/sendMessage", RawQuery: url.Values{"chat_id": {chatID}}.Encode()}
	return u, "chat " + chatID, nil
}

// Fingerprint identifies a URL for duplicate detection without storing it in clear.
func Fingerprint(u *url.URL) string {
	sum := sha256.Sum256([]byte(u.String()))
	return hex.EncodeToString(sum[:])
}

// NewSigningSecret returns a fresh random secret for signing webhook requests.
func NewSigningSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system CSPRNG failing is unrecoverable
	}
	return signingKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// Sign returns the signature header value for a request: HMAC-SHA256 over "<timestamp>.<body>", hex-encoded,
// so a receiver can check both authenticity and freshness.
func Sign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// WebhookPayload is the JSON body sent to a webhook destination.
type WebhookPayload struct {
	// outage, recovery, ssl_expiry, domain_expiry, slow, slow_recovery, dns_change, dns_change_closed, flapping, flapping_ended or test
	Event   string         `json:"event"`
	SentAt  time.Time      `json:"sent_at"`
	Monitor *PayloadTarget `json:"monitor,omitempty"`
	// Incident is set for outage and recovery events.
	Incident *PayloadIncident `json:"incident,omitempty"`
	// Certificate is set for ssl_expiry events.
	Certificate *PayloadCertificate `json:"certificate,omitempty"`
	// Domain is set for domain_expiry events.
	Domain *PayloadDomain `json:"domain,omitempty"`
	// Slowdown is set for slow and slow_recovery events.
	Slowdown *PayloadSlowdown `json:"slowdown,omitempty"`
	// DNSChange is set for dns_change and dns_change_closed events.
	DNSChange *PayloadDNSChange `json:"dns_change,omitempty"`
	// Flapping is set for flapping and flapping_ended events, and for the outage and recovery of an incident that began while the monitor was
	// flapping (it is alerted once the monitor stays down).
	Flapping *PayloadFlapping `json:"flapping,omitempty"`
	URL      string           `json:"url"` // where to look in HawkEye
}

type PayloadFlapping struct {
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Outages   int        `json:"outages"`
	Status    string     `json:"status"` // open, settled, stayed_down, or ended (flap suppression turned off, or the monitor paused)
}

type PayloadTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // http, or heartbeat for a cron job monitor
	URL  string `json:"url"`  // empty for heartbeats
}

type PayloadIncident struct {
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Cause      string     `json:"cause,omitempty"`
	// Snapshot is what the confirming check saw, on outage events of website and API monitors. Never the response body.
	Snapshot *PayloadSnapshot `json:"snapshot,omitempty"`
}

type PayloadSnapshot struct {
	Response  checks.ResponseFacts   `json:"response"`
	Step      string                 `json:"failed_step,omitempty"`
	Changes   []checks.Change        `json:"changes"`
	Locations []checks.LocationState `json:"locations,omitempty"`
}

type PayloadCertificate struct {
	ExpiresAt time.Time `json:"expires_at"`
	Bucket    int       `json:"warning_days"` // 30, 14, 7, 1, or 0 once expired
}

type PayloadSlowdown struct {
	StartedAt   time.Time  `json:"started_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	ThresholdMs int        `json:"threshold_ms"`
	PeakMs      int        `json:"peak_ms"`         // the slowest successful check while it lasted
	Cause       string     `json:"cause,omitempty"` // what got slower, compared with the past week
}

type PayloadDNSChange struct {
	RecordType string     `json:"record_type"`
	Before     []string   `json:"before"`
	After      []string   `json:"after"`
	Status     string     `json:"status"` // open, accepted or reverted
	DetectedAt time.Time  `json:"detected_at"`
	ClosedAt   *time.Time `json:"closed_at"`
	AcceptedBy string     `json:"accepted_by,omitempty"`
}

type PayloadDomain struct {
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expires_at"`
	Bucket    int       `json:"warning_days"` // 30, 14, 7, 1, or 0 once expired
}

func buildWebhook(a Alert, now time.Time) WebhookPayload {
	p := WebhookPayload{Event: a.Event, SentAt: now.UTC(), URL: a.AppBaseURL + "/incidents"}
	if a.Event == EventTest {
		p.URL = a.AppBaseURL + "/notifications"
		return p
	}
	if a.Event == EventDomainExpiry {
		p.URL = a.AppBaseURL + "/domains"
		p.Domain = &PayloadDomain{Name: a.DomainName, ExpiresAt: a.DomainExpiresAt.UTC(), Bucket: a.DomainBucket}
		return p
	}
	p.Monitor = &PayloadTarget{ID: a.MonitorID, Name: a.MonitorName, Type: a.monitorType(), URL: a.MonitorURL}
	if a.FlappingSince != nil {
		p.Flapping = &PayloadFlapping{StartedAt: a.FlappingSince.UTC(), Outages: a.FlapOutages, Status: a.FlapStatus}
	}
	switch a.Event {
	case EventFlapping, EventFlappingEnded:
		p.URL = a.AppBaseURL + "/monitors/" + a.MonitorID
		if a.ResolvedAt != nil {
			r := a.ResolvedAt.UTC()
			p.Flapping.EndedAt = &r
		}
	case EventSSLExpiry:
		p.URL = a.AppBaseURL + "/monitors/" + a.MonitorID
		p.Certificate = &PayloadCertificate{ExpiresAt: a.SSLExpiresAt.UTC(), Bucket: a.SSLBucket}
	case EventDNSChange, EventDNSChangeClosed:
		p.URL = a.AppBaseURL + "/monitors/" + a.MonitorID
		p.DNSChange = &PayloadDNSChange{RecordType: a.DNSRecordType, Before: a.DNSBefore, After: a.DNSAfter, Status: a.DNSStatus,
			DetectedAt: a.StartedAt.UTC(), AcceptedBy: a.DNSAcceptedBy}
		if a.ResolvedAt != nil {
			r := a.ResolvedAt.UTC()
			p.DNSChange.ClosedAt = &r
		}
	case EventSlow, EventSlowRecovery:
		p.URL = a.AppBaseURL + "/monitors/" + a.MonitorID
		p.Slowdown = &PayloadSlowdown{StartedAt: a.StartedAt.UTC(), ThresholdMs: a.SlowThresholdMs, PeakMs: a.SlowPeakMs, Cause: a.SlowCause}
		if a.Event == EventSlowRecovery && a.ResolvedAt != nil {
			r := a.ResolvedAt.UTC()
			p.Slowdown.ResolvedAt = &r
		}
	default:
		p.Incident = &PayloadIncident{StartedAt: a.StartedAt.UTC(), Cause: a.Cause}
		if sn := a.Snapshot; sn != nil {
			p.Incident.Snapshot = &PayloadSnapshot{Response: sn.Facts, Step: sn.StepName, Changes: sn.Changes, Locations: sn.Locations}
			if p.Incident.Snapshot.Changes == nil {
				p.Incident.Snapshot.Changes = []checks.Change{}
			}
		}
		if a.ResolvedAt != nil {
			r := a.ResolvedAt.UTC()
			p.Incident.ResolvedAt = &r
		}
	}
	return p
}

// tone is how serious a chat message looks (its color, where the chat app has one).
type tone int

const (
	toneGood tone = iota
	toneBad
	toneWarning
)

// chatMessage is what every chat channel shows: a title, the monitored address, detail lines and a link back to HawkEye.
// Its text is cleaned of control characters but not escaped: each channel escapes it for its own markup.
type chatMessage struct {
	Title  string
	Target string // the monitor's URL, if the alert is about a monitor
	Lines  []string
	Link   string
	Tone   tone
}

func chatContent(a Alert, now time.Time) chatMessage {
	if a.Event == EventDomainExpiry {
		name := clean(a.DomainName)
		m := chatMessage{Title: "🌐 The domain " + name + " expires in " + sslLeft(a.DomainExpiresAt, now), Tone: toneWarning}
		if a.DomainBucket == 0 {
			m.Title, m.Tone = "🔴 The domain "+name+" has expired", toneBad
		}
		m.Lines = []string{name, "Expires " + a.DomainExpiresAt.UTC().Format("2006-01-02 15:04 UTC")}
		m.Link = a.AppBaseURL + "/domains"
		return m
	}
	if len(a.Group) > 0 {
		return groupChat(a)
	}
	name := clean(a.MonitorName)
	m := chatMessage{Target: clean(a.MonitorURL), Link: a.AppBaseURL + "/incidents"}
	if a.Event == EventFlapping || a.Event == EventFlappingEnded {
		return flapChat(a, m)
	}
	switch a.Event {
	case EventTest:
		m = chatMessage{Title: "✅ HawkEye test alert", Lines: []string{"This destination is set up. You will get alerts here when a monitor goes offline or recovers."},
			Link: a.AppBaseURL + "/notifications", Tone: toneGood}
	case EventRecovery:
		m.Title, m.Tone = "✅ "+name+" is back online", toneGood
		if a.IsHeartbeat() {
			m.Title = "✅ " + name + " is checking in again"
		}
		if a.IsDNS() {
			m.Title = "✅ DNS for " + name + " is resolving correctly again"
		}
		if a.ResolvedAt != nil {
			m.Lines = []string{"Offline for " + Duration(a.ResolvedAt.Sub(a.StartedAt))}
			if a.IsHeartbeat() {
				m.Lines = []string{"Overdue or failing for " + Duration(a.ResolvedAt.Sub(a.StartedAt))}
			}
			if a.IsDNS() {
				m.Lines = []string{"Failing for " + Duration(a.ResolvedAt.Sub(a.StartedAt))}
			}
		}
	case EventDNSChange:
		m.Title, m.Tone = "🔀 DNS record changed for "+clean(a.MonitorURL), toneWarning
		m.Lines = []string{a.DNSRecordType + " record: " + answerList(a.DNSBefore) + " → " + answerList(a.DNSAfter),
			"If this wasn’t intended, check your registrar and DNS provider accounts."}
		m.Link = a.AppBaseURL + "/monitors/" + a.MonitorID
	case EventDNSChangeClosed:
		m.Title, m.Tone = "✅ DNS change for "+clean(a.MonitorURL)+" closed", toneGood
		m.Lines = []string{"The " + a.DNSRecordType + " record changed back."}
		if a.DNSStatus == "accepted" {
			who := "Someone on your team"
			if a.DNSAcceptedBy != "" {
				who = clean(a.DNSAcceptedBy)
			}
			m.Lines = []string{who + " marked it as intended: " + answerList(a.DNSAfter)}
		}
		m.Link = a.AppBaseURL + "/monitors/" + a.MonitorID
	case EventSlow:
		m.Title, m.Tone = "🐢 "+name+" is responding slowly", toneWarning
		m.Lines = []string{"Online, but over " + Milliseconds(a.SlowThresholdMs) + " for several checks in a row (slowest " + Milliseconds(a.SlowPeakMs) + ")"}
		if a.SlowCause != "" {
			m.Lines = append(m.Lines, a.SlowCause)
		}
		m.Link = a.AppBaseURL + "/monitors/" + a.MonitorID
	case EventSlowRecovery:
		m.Title, m.Tone = "✅ "+name+" is responding normally again", toneGood
		m.Lines = []string{"Back under " + Milliseconds(a.SlowThresholdMs)}
		if a.ResolvedAt != nil {
			m.Lines[0] += " after " + Duration(a.ResolvedAt.Sub(a.StartedAt)) + " (slowest " + Milliseconds(a.SlowPeakMs) + ")"
		}
		m.Link = a.AppBaseURL + "/monitors/" + a.MonitorID
	case EventSSLExpiry:
		m.Title, m.Tone = "🔒 SSL certificate for "+name+" expires in "+sslLeft(a.SSLExpiresAt, now), toneWarning
		if a.SSLBucket == 0 {
			m.Title, m.Tone = "🔴 SSL certificate for "+name+" has expired", toneBad
		}
		m.Lines = []string{"Expires " + a.SSLExpiresAt.UTC().Format("2006-01-02 15:04 UTC")}
		m.Link = a.AppBaseURL + "/monitors/" + a.MonitorID
	default:
		m.Title, m.Tone = "🔴 "+name+" may be offline", toneBad
		if a.IsHeartbeat() {
			m.Title = "🔴 " + name + " " + heartbeatProblem(a.Cause)
		}
		if a.IsDNS() {
			m.Title = "🔴 DNS check failing for " + name
		}
		m.Lines = []string{"Detected " + a.StartedAt.UTC().Format("2006-01-02 15:04 UTC")}
		if a.Cause != "" {
			m.Lines = append(m.Lines, clean(a.Cause))
		}
		if a.Snapshot != nil {
			m.Lines = append(m.Lines, snapshotChatLines(a.Snapshot)...)
		}
		if note := flapChatNote(a); note != "" {
			m.Lines = append(m.Lines, note)
		}
	}
	return m
}

// details is the target (if any) followed by the detail lines, each passed through esc.
func (m chatMessage) details(esc func(string) string) []string {
	var out []string
	if m.Target != "" {
		out = append(out, esc(m.Target))
	}
	for _, l := range m.Lines {
		out = append(out, esc(l))
	}
	return out
}

// slackEscape neutralizes the three characters Slack treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// slackMessage renders an alert as a Slack incoming-webhook payload: a one-line fallback plus one block.
func slackMessage(m chatMessage) map[string]any {
	title := slackEscape(m.Title)
	text := "*" + title + "*\n" + strings.Join(m.details(slackEscape), "\n") + "\n<" + m.Link + "|Open in HawkEye>"
	return map[string]any{
		"text":   strings.ReplaceAll(title, "*", ""),
		"blocks": []any{map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text}}},
	}
}

// discordEscape backslash-escapes Discord's markdown punctuation, so a monitor name cannot format the message or hide a link.
var discordEscape = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "|", `\|`, ">", `\>`,
	"[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "#", `\#`, "-", `\-`, ":", `\:`, "@", `\@`).Replace

var discordColors = map[tone]int{toneGood: 0x2e7d32, toneBad: 0xc62828, toneWarning: 0xe0a800}

// discordMessage renders an alert as a Discord webhook payload: one embed in the alert's color. Mentions are switched off,
// so text such as @everyone in a monitor name never pings anyone.
func discordMessage(m chatMessage) map[string]any {
	// The target stays unescaped so Discord links it; a validated URL has no spaces, so it is matched whole as a link.
	lines := m.details(discordEscape)
	if m.Target != "" {
		lines[0] = m.Target
	}
	lines = append(lines, "[Open in HawkEye]("+m.Link+")")
	// Embed titles cannot hold links and may not honor backslash escapes, so the title is only shortened to Discord's limit.
	title := []rune(m.Title)
	if len(title) > 256 {
		title = title[:256]
	}
	return map[string]any{
		"embeds":           []any{map[string]any{"title": string(title), "url": m.Link, "description": strings.Join(lines, "\n"), "color": discordColors[m.Tone]}},
		"allowed_mentions": map[string]any{"parse": []string{}},
	}
}

// teamsEscape breaks markdown link syntax. Adaptive Cards render a small markdown subset (bold, italic, lists, links) and have
// no reliable escape character, so the one construct that matters, a link hidden behind other text, is defused instead.
func teamsEscape(s string) string { return strings.ReplaceAll(s, "](", "] (") }

var teamsColors = map[tone]string{toneGood: "Good", toneBad: "Attention", toneWarning: "Warning"}

// teamsMessage renders an alert as an Adaptive Card message, the format Teams Workflows webhooks post to a channel.
func teamsMessage(m chatMessage) map[string]any {
	body := []any{map[string]any{"type": "TextBlock", "text": teamsEscape(m.Title), "weight": "Bolder", "size": "Medium", "wrap": true, "color": teamsColors[m.Tone]}}
	for _, l := range m.details(teamsEscape) {
		body = append(body, map[string]any{"type": "TextBlock", "text": l, "wrap": true, "spacing": "Small"})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "type": "AdaptiveCard", "version": "1.4",
		"body":    body,
		"actions": []any{map[string]any{"type": "Action.OpenUrl", "title": "Open in HawkEye", "url": m.Link}},
		"msteams": map[string]any{"width": "Full"},
	}
	return map[string]any{
		"type":        "message",
		"attachments": []any{map[string]any{"contentType": "application/vnd.microsoft.card.adaptive", "contentUrl": nil, "content": card}},
	}
}

// telegramMessage renders an alert as a Bot API sendMessage request in HTML mode, where only <, > and & (and quotes in
// attributes) need escaping.
func telegramMessage(chatID string, m chatMessage) map[string]any {
	lines := append([]string{"<b>" + html.EscapeString(m.Title) + "</b>"}, m.details(html.EscapeString)...)
	lines = append(lines, `<a href="`+html.EscapeString(m.Link)+`">Open in HawkEye</a>`)
	return map[string]any{
		"chat_id": chatID, "text": strings.Join(lines, "\n"), "parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	}
}

// buildRequest returns where to send one delivery, its body and its headers. target is the destination's stored URL;
// secret signs webhook requests and is ignored for the chat channels.
func buildRequest(channel, target string, a Alert, secret, deliveryID string, now time.Time) (string, []byte, map[string]string, error) {
	h := map[string]string{"Content-Type": "application/json", "User-Agent": "HawkEye-Alerts/1", eventHeader: a.Event}
	var msg any
	switch channel {
	case ChannelWebhook:
		body, err := json.Marshal(buildWebhook(a, now))
		if err != nil {
			return "", nil, nil, err
		}
		ts := now.Unix()
		h[timestampHeader] = strconv.FormatInt(ts, 10)
		h[signatureHeader] = Sign(secret, ts, body)
		if deliveryID != "" {
			h[deliveryHeader] = deliveryID // stable across retries, so receivers can drop duplicates
		}
		return target, body, h, nil
	case ChannelSlack:
		msg = slackMessage(chatContent(a, now))
	case ChannelDiscord:
		msg = discordMessage(chatContent(a, now))
	case ChannelTeams:
		msg = teamsMessage(chatContent(a, now))
	case ChannelTelegram:
		u, err := url.Parse(target)
		if err != nil {
			return "", nil, nil, err
		}
		chatID := u.Query().Get("chat_id")
		u.RawQuery = ""
		target, msg = u.String(), telegramMessage(chatID, chatContent(a, now))
	case ChannelPagerDuty:
		u, err := url.Parse(target)
		if err != nil {
			return "", nil, nil, err
		}
		key := u.Query().Get("routing_key")
		u.RawQuery = ""
		target, msg = u.String(), pagerDutyEvent(key, a, chatContent(a, now))
	default:
		return "", nil, nil, fmt.Errorf("unknown channel %q", channel)
	}
	body, err := json.Marshal(msg)
	return target, body, h, err
}

// post sends one request and classifies the outcome. Errors never contain the URL (its path is a credential)
// or the response body.
func post(ctx context.Context, c *http.Client, target string, body []byte, header map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return permanent("invalid URL")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err // drop the URL that net/http puts in the message
		}
		if errors.Is(err, safehttp.ErrBlocked) {
			return permanent("destination not permitted")
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return statusError{resp.StatusCode}
}

// statusError is a non-2xx answer from the receiver. 408, 429 and 5xx are worth retrying; any other status is permanent.
type statusError struct{ code int }

func (e statusError) Error() string { return fmt.Sprintf("receiver returned HTTP %d", e.code) }

func (e statusError) Is(target error) bool {
	return target == errPermanent && e.code != http.StatusRequestTimeout && e.code != http.StatusTooManyRequests && e.code < 500
}
