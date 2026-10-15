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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hawkeye/internal/safehttp"
	"hawkeye/internal/validation"
)

// Channels.
const (
	ChannelEmail   = "email"
	ChannelWebhook = "webhook"
	ChannelSlack   = "slack"
)

// EventTest is sent once when a webhook or Slack destination is added, to prove it works.
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

// NewHTTPClient returns the client used to reach webhook and Slack URLs: the same SSRF-safe client the monitors use,
// because these URLs are chosen by customers. Tests pass their own.
func NewHTTPClient() *http.Client { return safehttp.New(safehttp.Config{}, postTimeout) }

// ParseURL validates a webhook or Slack URL and returns it with a masked label that is safe to display
// (the path of these URLs is a credential).
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
	if channel == ChannelSlack && (host != "hooks.slack.com" || !strings.HasPrefix(u.Path, "/services/")) {
		return bad("must be a Slack incoming webhook URL (https://hooks.slack.com/services/…)")
	}
	return u, host + "/…", nil
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
	Event   string         `json:"event"` // outage, recovery, ssl_expiry or test
	SentAt  time.Time      `json:"sent_at"`
	Monitor *PayloadTarget `json:"monitor,omitempty"`
	// Incident is set for outage and recovery events.
	Incident *PayloadIncident `json:"incident,omitempty"`
	// Certificate is set for ssl_expiry events.
	Certificate *PayloadCertificate `json:"certificate,omitempty"`
	URL         string              `json:"url"` // where to look in HawkEye
}

type PayloadTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type PayloadIncident struct {
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Cause      string     `json:"cause,omitempty"`
}

type PayloadCertificate struct {
	ExpiresAt time.Time `json:"expires_at"`
	Bucket    int       `json:"warning_days"` // 30, 14, 7, 1, or 0 once expired
}

func buildWebhook(a Alert, now time.Time) WebhookPayload {
	p := WebhookPayload{Event: a.Event, SentAt: now.UTC(), URL: a.AppBaseURL + "/incidents"}
	if a.Event == EventTest {
		p.URL = a.AppBaseURL + "/notifications"
		return p
	}
	p.Monitor = &PayloadTarget{ID: a.MonitorID, Name: a.MonitorName, URL: a.MonitorURL}
	switch a.Event {
	case EventSSLExpiry:
		p.URL = a.AppBaseURL + "/monitors/" + a.MonitorID
		p.Certificate = &PayloadCertificate{ExpiresAt: a.SSLExpiresAt.UTC(), Bucket: a.SSLBucket}
	default:
		p.Incident = &PayloadIncident{StartedAt: a.StartedAt.UTC(), Cause: a.Cause}
		if a.ResolvedAt != nil {
			r := a.ResolvedAt.UTC()
			p.Incident.ResolvedAt = &r
		}
	}
	return p
}

// slackEscape neutralises the three characters Slack treats as markup.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// slackMessage renders an alert as a Slack incoming-webhook payload: a one-line fallback plus one block.
func slackMessage(a Alert, now time.Time) map[string]any {
	name, target := slackEscape(clean(a.MonitorName)), slackEscape(clean(a.MonitorURL))
	var title, detail, link string
	switch a.Event {
	case EventTest:
		title, detail = "✅ HawkEye test alert", "This destination is set up. You will get alerts here when a monitor goes down or recovers."
		link = a.AppBaseURL + "/notifications"
	case EventRecovery:
		title = "✅ " + name + " is responding again"
		detail = target
		if a.ResolvedAt != nil {
			detail += "\nDown for " + Duration(a.ResolvedAt.Sub(a.StartedAt))
		}
		link = a.AppBaseURL + "/incidents"
	case EventSSLExpiry:
		left := sslLeft(a.SSLExpiresAt, now)
		title = "🔒 SSL certificate for " + name + " expires in " + left
		if a.SSLBucket == 0 {
			title = "🔴 SSL certificate for " + name + " has expired"
		}
		detail = target + "\nExpires " + a.SSLExpiresAt.UTC().Format("2006-01-02 15:04 UTC")
		link = a.AppBaseURL + "/monitors/" + a.MonitorID
	default:
		title = "🔴 " + name + " may be down"
		detail = target + "\nDetected " + a.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
		if a.Cause != "" {
			detail += "\n" + slackEscape(clean(a.Cause))
		}
		link = a.AppBaseURL + "/incidents"
	}
	text := "*" + title + "*\n" + detail + "\n<" + link + "|Open in HawkEye>"
	return map[string]any{
		"text":   strings.ReplaceAll(title, "*", ""),
		"blocks": []any{map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text}}},
	}
}

// buildRequest returns the body and headers for one delivery. secret signs webhook requests and is ignored for Slack.
func buildRequest(channel string, a Alert, secret, deliveryID string, now time.Time) ([]byte, map[string]string, error) {
	h := map[string]string{"Content-Type": "application/json", "User-Agent": "HawkEye-Alerts/1", eventHeader: a.Event}
	if channel == ChannelSlack {
		body, err := json.Marshal(slackMessage(a, now))
		return body, h, err
	}
	body, err := json.Marshal(buildWebhook(a, now))
	if err != nil {
		return nil, nil, err
	}
	ts := now.Unix()
	h[timestampHeader] = strconv.FormatInt(ts, 10)
	h[signatureHeader] = Sign(secret, ts, body)
	if deliveryID != "" {
		h[deliveryHeader] = deliveryID // stable across retries, so receivers can drop duplicates
	}
	return body, h, nil
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
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("receiver returned HTTP %d", resp.StatusCode)
	default:
		return permanent("receiver returned HTTP %d", resp.StatusCode)
	}
}
