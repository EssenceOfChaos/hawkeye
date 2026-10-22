package notifications

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseURL(t *testing.T) {
	cases := []struct {
		channel, in string
		ok          bool
	}{
		{ChannelWebhook, "https://example.com/hooks/abc", true},
		{ChannelWebhook, "https://example.com:443/hooks/abc", true},
		{ChannelWebhook, "  https://example.com/x  ", true},
		{ChannelWebhook, "http://example.com/hooks", false},
		{ChannelWebhook, "https://example.com:8443/hooks", false},
		{ChannelWebhook, "https://user:pw@example.com/hooks", false},
		{ChannelWebhook, "https://127.0.0.1/hooks", false},
		{ChannelWebhook, "https://[::1]/hooks", false},
		{ChannelWebhook, "ftp://example.com", false},
		{ChannelWebhook, "not a url", false},
		{ChannelWebhook, "", false},
		{ChannelWebhook, "https://example.com/" + strings.Repeat("a", 3000), false},
		{ChannelSlack, "https://hooks.slack.com/services/T000/B000/XXXX", true},
		{ChannelSlack, "https://HOOKS.slack.com/services/T000/B000/XXXX", true},
		{ChannelSlack, "https://hooks.slack.com/other/T000", false},
		{ChannelSlack, "https://evil.example.com/services/T000/B000/XXXX", false},
		{ChannelSlack, "https://hooks.slack.com.evil.example/services/T/B/X", false},
	}
	for _, c := range cases {
		_, label, err := ParseURL(c.channel, c.in)
		if (err == nil) != c.ok {
			t.Errorf("%s %q: err=%v, want ok=%v", c.channel, c.in, err, c.ok)
		}
		if err == nil && (strings.Contains(label, "abc") || strings.Contains(label, "XXXX") || !strings.HasSuffix(label, "/…")) {
			t.Errorf("label %q must be only the host", label)
		}
	}
}

func TestSignatureCoversTimestampAndBody(t *testing.T) {
	body := []byte(`{"event":"outage"}`)
	a := Sign("whsec_x", 1700000000, body)
	if a != Sign("whsec_x", 1700000000, body) || !strings.HasPrefix(a, "sha256=") {
		t.Fatalf("not deterministic: %s", a)
	}
	for name, other := range map[string]string{
		"secret":    Sign("whsec_y", 1700000000, body),
		"timestamp": Sign("whsec_x", 1700000001, body),
		"body":      Sign("whsec_x", 1700000000, []byte(`{"event":"recovery"}`)),
	} {
		if other == a {
			t.Errorf("signature ignores the %s", name)
		}
	}
}

func TestWebhookPayloadShapes(t *testing.T) {
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	start := now.Add(-10 * time.Minute)
	end := now
	out := buildWebhook(Alert{Event: EventOutage, MonitorID: "m1", MonitorName: "Site", MonitorURL: "https://x.test", Cause: "HTTP 503", StartedAt: start, AppBaseURL: "https://app"}, now)
	if out.Event != "outage" || out.Monitor.ID != "m1" || out.Incident == nil || out.Incident.ResolvedAt != nil || out.Certificate != nil || out.URL != "https://app/incidents" {
		t.Fatalf("%+v", out)
	}
	rec := buildWebhook(Alert{Event: EventRecovery, MonitorID: "m1", StartedAt: start, ResolvedAt: &end, AppBaseURL: "https://app"}, now)
	if rec.Incident == nil || rec.Incident.ResolvedAt == nil {
		t.Fatalf("%+v", rec)
	}
	ssl := buildWebhook(Alert{Event: EventSSLExpiry, MonitorID: "m1", SSLExpiresAt: now.Add(48 * time.Hour), SSLBucket: 7, AppBaseURL: "https://app"}, now)
	if ssl.Certificate == nil || ssl.Certificate.Bucket != 7 || ssl.Incident != nil || ssl.URL != "https://app/monitors/m1" {
		t.Fatalf("%+v", ssl)
	}
	dom := buildWebhook(Alert{Event: EventDomainExpiry, DomainName: "example.com", DomainExpiresAt: now.Add(48 * time.Hour), DomainBucket: 7, AppBaseURL: "https://app"}, now)
	if dom.Domain == nil || dom.Domain.Name != "example.com" || dom.Domain.Bucket != 7 || dom.Monitor != nil || dom.Incident != nil || dom.Certificate != nil || dom.URL != "https://app/domains" {
		t.Fatalf("%+v", dom)
	}
	if test := buildWebhook(Alert{Event: EventTest, AppBaseURL: "https://app"}, now); test.Monitor != nil || test.Event != "test" {
		t.Fatalf("%+v", test)
	}
}

func TestSlackMessageEscapesMarkup(t *testing.T) {
	m := slackMessage(Alert{Event: EventOutage, MonitorName: "<!channel> & co", MonitorURL: "https://x.test/?a=1&b=<2>", StartedAt: time.Now(), AppBaseURL: "https://app"}, time.Now())
	text := m["blocks"].([]any)[0].(map[string]any)["text"].(map[string]any)["text"].(string)
	if strings.Contains(text, "<!channel>") || !strings.Contains(text, "&lt;!channel&gt; &amp; co") || !strings.Contains(text, "<https://app/incidents|Open in HawkEye>") {
		t.Fatalf("%s", text)
	}
	if !strings.Contains(m["text"].(string), "may be offline") {
		t.Fatalf("fallback text: %v", m["text"])
	}
}

func TestPostClassifiesResponsesAndHidesTheURL(t *testing.T) {
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("secret-body"))
	}))
	defer srv.Close()
	for code, wantPermanent := range map[int]bool{200: false, 204: false, 400: true, 404: true, 410: true, 408: false, 429: false, 500: false, 503: false} {
		status = code
		err := post(context.Background(), srv.Client(), srv.URL+"/secret/path", []byte(`{}`), nil)
		switch {
		case code < 300 && err != nil:
			t.Errorf("%d: %v", code, err)
		case code >= 300 && err == nil:
			t.Errorf("%d: expected an error", code)
		case code >= 300 && (errors.Is(err, errPermanent) != wantPermanent || strings.Contains(err.Error(), "secret")):
			t.Errorf("%d: permanent=%v err=%q", code, errors.Is(err, errPermanent), err)
		}
	}
	srv.Close()
	err := post(context.Background(), srv.Client(), srv.URL+"/secret/path", []byte(`{}`), nil)
	if err == nil || errors.Is(err, errPermanent) || strings.Contains(err.Error(), "secret/path") || strings.Contains(err.Error(), "http://") {
		t.Fatalf("a network failure must be retryable and must not echo the URL: %v", err)
	}
}

func TestSlackMessageForDomainExpiry(t *testing.T) {
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	m := slackMessage(Alert{Event: EventDomainExpiry, DomainName: "<b>x</b>.com", DomainExpiresAt: now.Add(10 * 24 * time.Hour), DomainBucket: 14, AppBaseURL: "https://app"}, now)
	text := m["blocks"].([]any)[0].(map[string]any)["text"].(map[string]any)["text"].(string)
	for _, want := range []string{"expires in 10 days", "&lt;b&gt;x&lt;/b&gt;.com", "<https://app/domains|Open in HawkEye>", "2026-10-22 15:00 UTC"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	expired := slackMessage(Alert{Event: EventDomainExpiry, DomainName: "example.com", DomainExpiresAt: now.Add(-time.Hour), DomainBucket: 0, AppBaseURL: "https://app"}, now)
	if !strings.Contains(expired["text"].(string), "has expired") {
		t.Fatalf("%v", expired["text"])
	}
}
