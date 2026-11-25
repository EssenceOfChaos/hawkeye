package notifications

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func groupOf(n int, event string) Alert {
	start := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	alerts := make([]Alert, n)
	for i := range alerts {
		resolved := start.Add(5 * time.Minute)
		alerts[i] = Alert{Event: event, MonitorID: "m" + strconv.Itoa(i), MonitorName: "Client " + strconv.Itoa(i),
			MonitorURL: "https://client" + strconv.Itoa(i) + ".example.com", Cause: "Connection refused", StartedAt: start,
			To: "agency@example.com", From: "alerts@example.com", AppBaseURL: "https://app.example.com"}
		if event == EventRecovery {
			alerts[i].ResolvedAt = &resolved
		}
	}
	return Group(alerts)
}

func TestGroupOfOneIsTheAlertItself(t *testing.T) {
	a := Alert{Event: EventOutage, MonitorName: "Shop"}
	if g := Group([]Alert{a}); len(g.Group) != 0 || g.MonitorName != "Shop" {
		t.Fatalf("%+v", g)
	}
}

func TestGroupedEmailListsTheMonitorsAndCountsTheRest(t *testing.T) {
	m := Render(groupOf(30, EventOutage))
	if m.Subject != "HawkEye alert: 30 monitors may be offline" || m.To != "agency@example.com" {
		t.Fatalf("%q to %q", m.Subject, m.To)
	}
	if !strings.Contains(m.Text, "Client 0") || !strings.Contains(m.Text, "Client 24") || strings.Contains(m.Text, "Client 25") {
		t.Fatalf("lists the first 25:\n%s", m.Text)
	}
	if !strings.Contains(m.Text, "And 5 monitors more") || !strings.Contains(m.Text, "/notifications") {
		t.Fatalf("counts the rest and says how to turn grouping off:\n%s", m.Text)
	}
	if r := Render(groupOf(2, EventRecovery)); r.Subject != "HawkEye recovery: 2 monitors are back online" || !strings.Contains(r.Text, "offline for 5m") {
		t.Fatalf("%q\n%s", r.Subject, r.Text)
	}
}

// Every chat channel gets one message, short enough for each app's limits however many monitors are in it.
func TestGroupedChatMessagesStayWithinLimits(t *testing.T) {
	g := groupOf(80, EventOutage)
	for i := range g.Group {
		g.Group[i].MonitorName = strings.Repeat("N", 300)
		g.Group[i].Cause = strings.Repeat("c", 300)
	}
	m := chatContent(g, time.Now())
	if m.Title != "🔴 80 monitors may be offline" || len(m.Lines) != groupListedChat+1 || m.Lines[len(m.Lines)-1] != "…and 70 monitors more" {
		t.Fatalf("%q %d %q", m.Title, len(m.Lines), m.Lines[len(m.Lines)-1])
	}
	for _, ch := range []string{ChannelSlack, ChannelDiscord, ChannelTeams, ChannelTelegram} {
		target := "https://hooks.example.com/x"
		if ch == ChannelTelegram {
			target = "https://api.telegram.org/botT/sendMessage?chat_id=1"
		}
		_, body, _, err := buildRequest(ch, target, g, "", "d", time.Now())
		if err != nil {
			t.Fatal(ch, err)
		}
		var msg struct {
			Text   string                         // Telegram: 4096 characters
			Embeds []struct{ Description string } // Discord: 4096 characters
		}
		json.Unmarshal(body, &msg)
		if n := len([]rune(msg.Text)); ch == ChannelTelegram && n > 4096 {
			t.Errorf("Telegram text is %d characters", n)
		}
		if ch == ChannelDiscord && len([]rune(msg.Embeds[0].Description)) > 4096 {
			t.Errorf("Discord description is %d characters", len([]rune(msg.Embeds[0].Description)))
		}
		if len(body) > 28000 {
			t.Errorf("%s message is %d bytes (Teams allows about 28 KB)", ch, len(body))
		}
	}
	var slack struct {
		Blocks []struct{ Text struct{ Text string } }
	}
	_, body, _, _ := buildRequest(ChannelSlack, "https://hooks.slack.com/x", g, "", "d", time.Now())
	json.Unmarshal(body, &slack)
	if n := len([]rune(slack.Blocks[0].Text.Text)); n > 3000 {
		t.Fatalf("Slack's section text limit is 3000 characters: %d", n)
	}
}

func TestGroupableKindsAndChannels(t *testing.T) {
	for _, tc := range []struct {
		event, channel string
		want           bool
	}{
		{EventOutage, ChannelEmail, true}, {EventRecovery, ChannelSlack, true}, {EventFlapping, ChannelTeams, true},
		{EventFlappingEnded, ChannelTelegram, true}, {EventOutage, ChannelDiscord, true},
		{EventOutage, ChannelWebhook, false}, {EventOutage, ChannelPagerDuty, false},
		{EventSlow, ChannelEmail, false}, {EventSSLExpiry, ChannelEmail, false}, {EventDNSChange, ChannelSlack, false},
	} {
		if got := groupable(tc.event, tc.channel); got != tc.want {
			t.Errorf("%s to %s: %v", tc.event, tc.channel, got)
		}
	}
}

func flapAlert(event, status string) Alert {
	since := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	return Alert{Event: event, MonitorID: "m1", MonitorName: "Shop", MonitorURL: "https://shop.example.com", Cause: "Received HTTP 502",
		StartedAt: since, FlapEpisodeID: "e1", FlappingSince: &since, FlapOutages: 3, FlapStatus: status, AppBaseURL: "https://app.example.com"}
}

func TestFlappingMessages(t *testing.T) {
	m := Render(flapAlert(EventFlapping, "open"))
	if m.Subject != "HawkEye alert: Shop is flapping" || !strings.Contains(m.Text, "3 outages") || !strings.Contains(m.Text, "straight away if it stays offline") {
		t.Fatalf("%q\n%s", m.Subject, m.Text)
	}
	if m := Render(flapAlert(EventFlappingEnded, "settled")); m.Subject != "HawkEye: Shop has stopped flapping" {
		t.Fatalf("%q", m.Subject)
	}
	if m := Render(flapAlert(EventFlappingEnded, "ended")); m.Subject != "HawkEye: outage alerts for Shop are back on" {
		t.Fatalf("%q", m.Subject)
	}
	c := chatContent(flapAlert(EventFlapping, "open"), time.Now())
	if c.Title != "🔁 Shop is flapping" || c.Tone != toneBad || c.Link != "https://app.example.com/monitors/m1" {
		t.Fatalf("%+v", c)
	}
	// The outage alert of a flapping monitor that stayed down says why it comes now.
	out := flapAlert(EventOutage, "stayed_down")
	if m := Render(out); !strings.Contains(m.Text, "had been flapping since 2026-11-24 09:00 UTC (3 outages)") {
		t.Fatalf("%s", m.Text)
	}
	if c := chatContent(out, time.Now()); c.Lines[len(c.Lines)-1] != "Flapping since 2026-11-24 09:00 UTC, and now staying offline" {
		t.Fatalf("%+v", c.Lines)
	}
	if m := Render(Alert{Event: EventOutage, MonitorName: "Shop"}); strings.Contains(m.Text, "flapping") {
		t.Fatal("an ordinary outage says nothing about flapping")
	}
}

// PagerDuty: the flapping alert opens one alert; it resolves when the monitor settles, or when the outage that stayed down recovers.
func TestPagerDutyFlappingSharesOneKey(t *testing.T) {
	want := "hawkeye:flapping:e1"
	for _, tc := range []struct {
		event, action string
	}{{EventFlapping, "trigger"}, {EventOutage, "trigger"}, {EventFlappingEnded, "resolve"}, {EventRecovery, "resolve"}} {
		_, ev := pdBuild(t, flapAlert(tc.event, "open"))
		if ev.DedupKey != want || ev.EventAction != tc.action {
			t.Errorf("%s: %s %s", tc.event, ev.DedupKey, ev.EventAction)
		}
	}
	_, ev := pdBuild(t, Alert{Event: EventOutage, MonitorID: "m1", MonitorName: "Shop", StartedAt: time.Unix(100, 0)})
	if ev.DedupKey != "hawkeye:incident:m1:100" {
		t.Fatalf("an ordinary outage keeps its own key: %s", ev.DedupKey)
	}
}

func TestWebhookCarriesTheFlapEpisode(t *testing.T) {
	p := buildWebhook(flapAlert(EventFlapping, "open"), time.Now())
	if p.Event != "flapping" || p.Flapping == nil || p.Flapping.Outages != 3 || p.Flapping.Status != "open" || p.Incident != nil {
		t.Fatalf("%+v", p)
	}
	if p := buildWebhook(Alert{Event: EventOutage, MonitorID: "m1"}, time.Now()); p.Flapping != nil || p.Incident == nil {
		t.Fatalf("%+v", p)
	}
}
