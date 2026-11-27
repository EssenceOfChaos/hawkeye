package notifications

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/checks"
)

func snapshotAlert() Alert {
	return Alert{Event: EventOutage, MonitorID: "m1", MonitorName: "API", MonitorURL: "https://api.example.com/health", Cause: "Received HTTP 502",
		StartedAt: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC), From: "alerts@example.com", To: "dev@example.com", AppBaseURL: "https://app.example.com",
		Snapshot: &AlertSnapshot{
			Facts: checks.ResponseFacts{StatusCode: 502, ContentType: "text/html", SizeBytes: 1200, ServerAddr: "198.51.100.9",
				FinalURL: "https://api.example.com/health", Headers: []checks.Header{{Name: "Server", Value: "cloudflare"}, {Name: "Cf-Ray", Value: "8f1a"}},
				Timings: &checks.Timings{DNSMs: 12, ConnectMs: 31, TLSMs: 48, WaitMs: 2410}},
			Changes: []checks.Change{{Field: "status", Before: "200", After: "502"}, {Field: "header", Name: "Server", Before: "nginx", After: "cloudflare"},
				{Field: "timing", Name: "wait", Before: "180 ms", After: "2.4 s"}},
			Locations: []checks.LocationState{{Region: "eu-central", Name: "Amsterdam", Status: "down", Error: "Received HTTP 502"},
				{Region: "us-east", Name: "New York", Status: "up"}},
		}}
}

func TestOutageEmailSaysWhatHawkEyeSaw(t *testing.T) {
	m := Render(snapshotAlert())
	for _, want := range []string{"HTTP 502, text/html, 1.2 KB", "198.51.100.9", "DNS 12 ms · connect 31 ms · TLS 48 ms · server wait 2,410 ms",
		"Amsterdam: Received HTTP 502 · New York: OK", "Changed since the last good check:", "• Status: 200 → 502", "• Server: nginx → cloudflare",
		"• Server wait: 2.4 s (usually 180 ms)", "Response headers:", "Cf-Ray: 8f1a"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("email lacks %q:\n%s", want, m.Text)
		}
	}
	// Without a snapshot (older incidents, heartbeats, DNS monitors), the email is as before.
	a := snapshotAlert()
	a.Snapshot = nil
	if m := Render(a); strings.Contains(m.Text, "Changed since") || strings.Contains(m.Text, "Timings") {
		t.Fatalf("%s", m.Text)
	}
}

// Header values come from the monitored site: they are quoted as plain text, never as markup.
func TestSnapshotHeaderValuesAreEscapedInTheEmail(t *testing.T) {
	a := snapshotAlert()
	a.Snapshot.Facts.Headers = []checks.Header{{Name: "Server", Value: `<a href="https://evil.example">click</a>`}}
	if m := Render(a); strings.Contains(m.HTML, `<a href="https://evil.example">`) {
		t.Fatal("a header value was rendered as HTML")
	}
}

func TestOutageChatSaysWhatHawkEyeSawInThreeLines(t *testing.T) {
	m := chatContent(snapshotAlert(), time.Now())
	got := strings.Join(m.Lines, "\n")
	want := strings.Join([]string{"Detected 2026-11-27 09:00 UTC", "Received HTTP 502", "DNS 12 ms · connect 31 ms · TLS 48 ms · server wait 2,410 ms",
		"Changed: Status: 200 → 502 · Server: nginx → cloudflare", "Amsterdam: Received HTTP 502 · New York: OK"}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// The longest snapshot still fits every chat app.
func TestSnapshotChatMessagesStayWithinLimits(t *testing.T) {
	a := snapshotAlert()
	a.MonitorName, a.Cause = strings.Repeat("N", 300), strings.Repeat("c", 300)
	a.Snapshot.StepName = strings.Repeat("s", 300)
	for i := range 20 {
		a.Snapshot.Changes = append(a.Snapshot.Changes, checks.Change{Field: "header", Name: "X-" + strconv.Itoa(i), Before: strings.Repeat("b", 200), After: strings.Repeat("a", 200)})
		a.Snapshot.Locations = append(a.Snapshot.Locations, checks.LocationState{Name: strings.Repeat("L", 40), Status: "down", Error: strings.Repeat("e", 120)})
	}
	m := chatContent(a, time.Now())
	if len(m.Lines) > 2+3 {
		t.Fatalf("%d lines", len(m.Lines))
	}
	for _, l := range m.Lines {
		if n := len([]rune(l)); n > maxChatLine {
			t.Fatalf("a %d-character line", n)
		}
	}
	for _, ch := range []string{ChannelSlack, ChannelDiscord, ChannelTeams, ChannelTelegram, ChannelPagerDuty} {
		target := "https://hooks.example.com/x"
		if ch == ChannelTelegram {
			target = "https://api.telegram.org/botT/sendMessage?chat_id=1"
		}
		if ch == ChannelPagerDuty {
			target = "https://events.pagerduty.com/v2/enqueue#routing_key=0123456789abcdef0123456789abcdef"
		}
		_, body, _, err := buildRequest(ch, target, a, "", "d", time.Now())
		if err != nil {
			t.Fatal(ch, err)
		}
		if len(body) > 8000 {
			t.Errorf("%s message is %d bytes", ch, len(body))
		}
	}
}

func TestWebhookCarriesTheSnapshotButNoBody(t *testing.T) {
	p := buildWebhook(snapshotAlert(), time.Now())
	if p.Incident == nil || p.Incident.Snapshot == nil || p.Incident.Snapshot.Response.StatusCode != 502 || len(p.Incident.Snapshot.Changes) != 3 {
		t.Fatalf("%+v", p.Incident)
	}
	b, _ := json.Marshal(p)
	for _, key := range []string{`"snapshot":`, `"response":`, `"server_addr":"198.51.100.9"`, `"changes":[`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("payload lacks %s: %s", key, b)
		}
	}
	if strings.Contains(string(b), "excerpt") || strings.Contains(string(b), `"body"`) {
		t.Fatalf("a webhook must never carry response text: %s", b)
	}
}
