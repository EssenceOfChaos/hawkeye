package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/validation"
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
		{ChannelDiscord, "https://discord.com/api/webhooks/123456/abc-XXXX_y", true},
		{ChannelDiscord, "https://discordapp.com/api/webhooks/123456/abcXXXX", true},
		{ChannelDiscord, "https://discord.com/api/v10/webhooks/123456/abcXXXX?thread_id=9", true},
		{ChannelDiscord, "https://discord.com/api/webhooks/123456/abcXXXX/slack", false}, // Slack-compatible endpoint expects Slack's format
		{ChannelDiscord, "https://discord.com/channels/123/456", false},
		{ChannelDiscord, "https://discord.com.evil.example/api/webhooks/123456/abcXXXX", false},
		{ChannelDiscord, "https://hooks.slack.com/services/T000/B000/XXXX", false},
		{ChannelTeams, "https://prod-12.westus.logic.azure.com:443/workflows/abc/triggers/manual/paths/invoke?sig=XXXX", true},
		{ChannelTeams, "https://default0a1b.2c.environment.api.powerplatform.com:443/powerautomate/automations/direct/workflows/abc/triggers/manual/paths/invoke?sig=XXXX", true},
		{ChannelTeams, "https://contoso.webhook.office.com/webhookb2/abc/IncomingWebhook/XXXX", false}, // retired Office 365 connector
		{ChannelTeams, "https://logic.azure.com.evil.example/workflows/abc", false},
		{ChannelTeams, "https://prod-12.westus.logic.azure.com/other/abc", false},
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
	m := slackMessage(chatContent(Alert{Event: EventOutage, MonitorName: "<!channel> & co", MonitorURL: "https://x.test/?a=1&b=<2>", StartedAt: time.Now(), AppBaseURL: "https://app"}, time.Now()))
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
	m := slackMessage(chatContent(Alert{Event: EventDomainExpiry, DomainName: "<b>x</b>.com", DomainExpiresAt: now.Add(10 * 24 * time.Hour), DomainBucket: 14, AppBaseURL: "https://app"}, now))
	text := m["blocks"].([]any)[0].(map[string]any)["text"].(map[string]any)["text"].(string)
	for _, want := range []string{"expires in 10 days", "&lt;b&gt;x&lt;/b&gt;.com", "<https://app/domains|Open in HawkEye>", "2026-10-22 15:00 UTC"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	expired := slackMessage(chatContent(Alert{Event: EventDomainExpiry, DomainName: "example.com", DomainExpiresAt: now.Add(-time.Hour), DomainBucket: 0, AppBaseURL: "https://app"}, now))
	if !strings.Contains(expired["text"].(string), "has expired") {
		t.Fatalf("%v", expired["text"])
	}
}

func TestTelegramURL(t *testing.T) {
	const token = "123456789:AAEhBP0av28MapQ3a_Bb-cDeFgHiJkLmNoP"
	cases := []struct {
		token, chat, field string
	}{
		{token, "-1001234567890", ""},
		{token, " 42 ", ""},
		{token, "@my_channel", ""},
		{"", "42", "bot_token"},
		{"not-a-token", "42", "bot_token"},
		{"123:short", "42", "bot_token"},
		{token + "/../x", "42", "bot_token"},
		{token, "", "destination"},
		{token, "chat 42", "destination"},
		{token, "42&text=hi", "destination"},
		{token, "@ab", "destination"},
	}
	for _, c := range cases {
		u, label, err := TelegramURL(c.token, c.chat)
		if c.field == "" {
			if err != nil || u.Host != "api.telegram.org" || u.Path != "/bot"+token+"/sendMessage" || strings.Contains(label, token) || !strings.HasPrefix(label, "chat ") {
				t.Errorf("%q %q: %v %v %q", c.token, c.chat, u, err, label)
			}
			continue
		}
		var verr *validation.Error
		if !errors.As(err, &verr) || verr.Fields[c.field] == "" {
			t.Errorf("%q %q: want an error on %s, got %v", c.token, c.chat, c.field, err)
		}
	}
}

func TestChatRequestsCarryTheAlertInEachFormat(t *testing.T) {
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	a := Alert{Event: EventOutage, MonitorID: "m1", MonitorName: "Shop [click](https://evil.test) @everyone", MonitorURL: "https://shop.test/a_b",
		Cause: "Received HTTP 503 <b>", StartedAt: now.Add(-time.Minute), AppBaseURL: "https://app"}

	to, body, h, err := buildRequest(ChannelDiscord, "https://discord.com/api/webhooks/1/x", a, "", "d1", now)
	if err != nil || to != "https://discord.com/api/webhooks/1/x" || h[signatureHeader] != "" {
		t.Fatalf("%s %v %v", to, h, err)
	}
	var d struct {
		Embeds []struct {
			Title, URL, Description string
			Color                   int
		}
		AllowedMentions struct{ Parse []string } `json:"allowed_mentions"`
	}
	mustUnmarshal(t, body, &d)
	if len(d.Embeds) != 1 || d.Embeds[0].Color != discordColors[toneBad] || d.Embeds[0].URL != "https://app/incidents" || !strings.Contains(d.Embeds[0].Title, "may be offline") {
		t.Fatalf("%s", body)
	}
	if desc := d.Embeds[0].Description; !strings.Contains(desc, "https://shop.test/a_b\n") || !strings.Contains(desc, "[Open in HawkEye](https://app/incidents)") || strings.Contains(desc, "Received HTTP 503 <b>") {
		t.Fatalf("description: %q", desc)
	}
	if d.AllowedMentions.Parse == nil || len(d.AllowedMentions.Parse) != 0 {
		t.Fatalf("mentions must be switched off: %s", body)
	}

	_, body, _, _ = buildRequest(ChannelTeams, "https://x.logic.azure.com/workflows/1", a, "", "", now)
	if s := string(body); !strings.Contains(s, `"application/vnd.microsoft.card.adaptive"`) || !strings.Contains(s, `"Action.OpenUrl"`) ||
		!strings.Contains(s, `"color":"Attention"`) || strings.Contains(s, "](https://evil.test)") || !strings.Contains(s, "] (https://evil.test)") {
		t.Fatalf("%s", s)
	}

	to, body, _, err = buildRequest(ChannelTelegram, "https://api.telegram.org/bot1:tok/sendMessage?chat_id=-100123", a, "", "", now)
	if err != nil || to != "https://api.telegram.org/bot1:tok/sendMessage" {
		t.Fatalf("the chat ID belongs in the body, not the URL: %s %v", to, err)
	}
	var tg struct {
		ChatID    string `json:"chat_id"`
		Text      string
		ParseMode string `json:"parse_mode"`
	}
	mustUnmarshal(t, body, &tg)
	if tg.ChatID != "-100123" || tg.ParseMode != "HTML" || !strings.HasPrefix(tg.Text, "<b>🔴 Shop") || strings.Contains(tg.Text, "503 <b>") ||
		!strings.Contains(tg.Text, "503 &lt;b&gt;") || !strings.HasSuffix(tg.Text, `<a href="https://app/incidents">Open in HawkEye</a>`) {
		t.Fatalf("%+v", tg)
	}
}

func TestDiscordEscapesMarkdownInTheDescription(t *testing.T) {
	m := discordMessage(chatMessage{Title: "t", Lines: []string{"[x](https://evil.test) **b** `c`"}, Link: "https://app"})
	desc := m["embeds"].([]any)[0].(map[string]any)["description"].(string)
	if strings.Contains(desc, "[x](") || strings.Contains(desc, "**") || !strings.Contains(desc, `\[x\]\(https\:`) {
		t.Fatalf("%q", desc)
	}
}

func TestChatContentCoversEveryEvent(t *testing.T) {
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	end := now
	for _, c := range []struct {
		a     Alert
		title string
		tone  tone
		link  string
	}{
		{Alert{Event: EventTest, AppBaseURL: "https://app"}, "test alert", toneGood, "https://app/notifications"},
		{Alert{Event: EventRecovery, MonitorName: "Site", StartedAt: now.Add(-5 * time.Minute), ResolvedAt: &end, AppBaseURL: "https://app"}, "back online", toneGood, "https://app/incidents"},
		{Alert{Event: EventSSLExpiry, MonitorID: "m1", MonitorName: "Site", SSLExpiresAt: now.Add(72 * time.Hour), SSLBucket: 7, AppBaseURL: "https://app"}, "expires in 3 days", toneWarning, "https://app/monitors/m1"},
		{Alert{Event: EventSSLExpiry, MonitorID: "m1", MonitorName: "Site", SSLExpiresAt: now.Add(-time.Hour), AppBaseURL: "https://app"}, "has expired", toneBad, "https://app/monitors/m1"},
		{Alert{Event: EventDomainExpiry, DomainName: "example.com", DomainExpiresAt: now.Add(72 * time.Hour), DomainBucket: 7, AppBaseURL: "https://app"}, "expires in 3 days", toneWarning, "https://app/domains"},
	} {
		m := chatContent(c.a, now)
		if !strings.Contains(m.Title, c.title) || m.Tone != c.tone || m.Link != c.link {
			t.Errorf("%s: %+v", c.a.Event, m)
		}
	}
}

func TestTelegramFailuresExplainTheLikelyMistake(t *testing.T) {
	for code, want := range map[int]string{400: "chat ID", 401: "bot token", 404: "bot token", 403: "not allowed"} {
		if r := testReason(ChannelTelegram, statusError{code}); !strings.Contains(r, want) || !strings.Contains(r, "HTTP") {
			t.Errorf("%d: %q", code, r)
		}
	}
	if r := testReason(ChannelDiscord, statusError{400}); r != "receiver returned HTTP 400" {
		t.Errorf("other channels get the plain reason: %q", r)
	}
}

func mustUnmarshal(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
}
