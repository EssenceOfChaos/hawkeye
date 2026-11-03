package notifications

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderOutage(t *testing.T) {
	m := Render(Alert{Event: EventOutage, MonitorName: "Client Site", MonitorURL: "https://client.example.com/",
		Cause: "Received HTTP 503", StartedAt: time.Date(2026, 10, 12, 14, 30, 0, 0, time.UTC),
		AppBaseURL: "https://app.example.com", To: "me@example.com", From: "alerts@example.com"})
	for _, want := range []string{"repeated failures while checking Client Site", "https://client.example.com/", "2026-10-12 14:30 UTC", "Received HTTP 503", "https://app.example.com/incidents", "/notifications"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("body missing %q:\n%s", want, m.Text)
		}
	}
	if m.Subject != "HawkEye alert: Client Site may be offline" || m.To != "me@example.com" || m.From != "alerts@example.com" {
		t.Fatalf("%+v", m)
	}
}

func TestRenderRecoveryIncludesDuration(t *testing.T) {
	start := time.Date(2026, 10, 12, 14, 30, 0, 0, time.UTC)
	end := start.Add(2*time.Hour + 5*time.Minute)
	m := Render(Alert{Event: EventRecovery, MonitorName: "Site", MonitorURL: "https://x.example.com", StartedAt: start, ResolvedAt: &end, AppBaseURL: "https://a"})
	if m.Subject != "HawkEye recovery: Site is back online" || !strings.Contains(m.Text, "2h 5m") {
		t.Fatalf("%+v", m)
	}
}

func TestRenderCannotInjectHeadersOrLines(t *testing.T) {
	m := Render(Alert{Event: EventOutage, MonitorName: "x\r\nBcc: evil@example.com\r\n\r\nbody", MonitorURL: "https://a.example.com/\nInjected: yes",
		Cause: "line1\r\nline2", AppBaseURL: "https://a"})
	if strings.ContainsAny(m.Subject, "\r\n") {
		t.Fatalf("subject contains a line break: %q", m.Subject)
	}
	for _, line := range strings.Split(m.Text, "\n") {
		if strings.HasPrefix(line, "Bcc:") || strings.HasPrefix(line, "Injected:") {
			t.Fatalf("injected line in body: %q", line)
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "30s", 5 * time.Minute: "5m", 90 * time.Minute: "1h 30m", 49 * time.Hour: "2d 1h",
	} {
		if got := Duration(d); got != want {
			t.Errorf("%v: %q want %q", d, got, want)
		}
	}
}

func TestDefaultBackoffGrowsAndSaturates(t *testing.T) {
	prev := time.Duration(0)
	for a := 1; a <= 5; a++ {
		if d := DefaultBackoff(a); d <= prev {
			t.Fatalf("backoff must increase: attempt %d = %v", a, d)
		} else {
			prev = d
		}
	}
	if DefaultBackoff(50) != DefaultBackoff(5) || DefaultBackoff(0) != DefaultBackoff(1) {
		t.Fatal("out-of-range attempts must clamp")
	}
}

// ALERT_PREVIEW_DIR=/some/dir go test ./internal/notifications -run TestWriteAlertPreview writes the real outage and recovery emails (as built by Render)
// as .html files for a look in a browser, or for the landing page screenshot (scripts/screenshots). ALERT_PREVIEW_BASE is where /email/hawkeye-logo.png is served.
func TestWriteAlertPreview(t *testing.T) {
	dir := os.Getenv("ALERT_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set ALERT_PREVIEW_DIR to write preview files")
	}
	base := os.Getenv("ALERT_PREVIEW_BASE")
	if base == "" {
		base = "https://app.example.com"
	}
	started := time.Date(2026, 10, 19, 14, 11, 0, 0, time.UTC)
	resolved := started.Add(14 * time.Minute)
	for name, a := range map[string]Alert{
		"alert-outage":   {Event: EventOutage, MonitorName: "Checkout", MonitorURL: "https://shop.example.com/checkout", Cause: "Request timed out", StartedAt: started, AppBaseURL: base, From: "alerts@hawkeye.observer", To: "owner@example.com"},
		"alert-test":     {Event: EventTest, To: "owner@example.com", From: "alerts@hawkeye.observer", AppBaseURL: base, RequestedBy: "owner@example.com", SentAt: started},
		"alert-recovery": {Event: EventRecovery, MonitorName: "Checkout", MonitorURL: "https://shop.example.com/checkout", StartedAt: started, ResolvedAt: &resolved, AppBaseURL: base, From: "alerts@hawkeye.observer", To: "owner@example.com"},
	} {
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(Render(a).HTML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTestAlertEmailIsUnmistakablyATest(t *testing.T) {
	m := Render(Alert{Event: EventTest, To: "ops@example.com", From: "alerts@example.com", AppBaseURL: "https://app.example.com", RequestedBy: "owner@example.com",
		SentAt: time.Date(2026, 10, 19, 15, 4, 0, 0, time.UTC)})
	if m.Subject != "HawkEye test alert" || m.To != "ops@example.com" || m.From != "alerts@example.com" {
		t.Fatalf("%+v", m)
	}
	for _, want := range []string{"Your alerts are working", "this is a test alert", "Nothing is wrong", "ops@example.com", "owner@example.com", "2026-10-19 15:04 UTC", "https://app.example.com/notifications"} {
		if !strings.Contains(strings.ToLower(m.Text), strings.ToLower(want)) {
			t.Errorf("text missing %q:\n%s", want, m.Text)
		}
	}
	// Nothing that reads like a real outage. (The closing note describes real alerts, so "may be offline" may appear there; the subject, heading and facts must not.)
	top := m.Text[:strings.Index(m.Text, "Sent to")]
	for _, notWanted := range []string{"may be offline", "repeated failures", "Incident", "Detected at", "Website:"} {
		if strings.Contains(top, notWanted) || strings.Contains(m.Subject, notWanted) {
			t.Errorf("a test alert must not say %q up front", notWanted)
		}
	}
	if strings.Contains(m.Text, "Website:") || strings.Contains(m.Text, "Incident status") {
		t.Error("a test alert has no website or incident")
	}
	if strings.Contains(m.HTML, "#ff8a80") || strings.Contains(m.HTML, "#56d364") {
		t.Error("a test alert uses the brand gold, never the outage red or recovery green")
	}
	if !strings.Contains(m.HTML, "#e8b95e") {
		t.Error("a test alert should carry the gold accent")
	}
	// Who asked is optional; without it the line is simply absent.
	if strings.Contains(Render(Alert{Event: EventTest, To: "a@example.com", AppBaseURL: "https://app.example.com"}).Text, "Requested by") {
		t.Error("no requester, no line")
	}
}

func TestRenderDomainExpiry(t *testing.T) {
	expires := time.Now().Add(20 * 24 * time.Hour)
	a := Alert{Event: EventDomainExpiry, DomainName: "example.com", DomainExpiresAt: expires, DomainBucket: 30, AppBaseURL: "https://app.example.com", To: "me@example.com", From: "alerts@example.com"}
	m := Render(a)
	if !strings.HasPrefix(m.Subject, "HawkEye reminder: the domain example.com expires in 19 days") && !strings.HasPrefix(m.Subject, "HawkEye reminder: the domain example.com expires in 20 days") {
		t.Fatalf("subject %q", m.Subject)
	}
	for _, want := range []string{"example.com", expires.UTC().Format("2006-01-02 15:04 UTC"), "https://app.example.com/domains", "remind you again"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("body missing %q:\n%s", want, m.Text)
		}
	}
	if strings.Contains(m.HTML, "#ff8a80") {
		t.Error("a 30-day warning is not urgent enough for the alert color")
	}

	a.DomainBucket = 1
	a.DomainExpiresAt = time.Now().Add(6 * time.Hour)
	m = Render(a)
	if m.Subject != "HawkEye reminder: the domain example.com expires within a day" || !strings.Contains(m.HTML, "#ff8a80") || strings.Contains(m.Text, "remind you again") {
		t.Fatalf("%q\n%s", m.Subject, m.Text)
	}

	a.DomainBucket = 0
	a.DomainExpiresAt = time.Now().Add(-time.Hour)
	m = Render(a)
	if m.Subject != "HawkEye alert: the domain example.com has expired" || !strings.Contains(m.Text, "Expired") {
		t.Fatalf("%q\n%s", m.Subject, m.Text)
	}
}

func TestRenderDomainExpiryCannotInjectLines(t *testing.T) {
	m := Render(Alert{Event: EventDomainExpiry, DomainName: "x.com\r\nBcc: evil@example.com", DomainExpiresAt: time.Now().Add(time.Hour), DomainBucket: 1, AppBaseURL: "https://a"})
	if strings.ContainsAny(m.Subject, "\r\n") {
		t.Fatalf("subject contains a line break: %q", m.Subject)
	}
	for _, line := range strings.Split(m.Text, "\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("injected line in body: %q", line)
		}
	}
}

func TestRenderSlowResponseAlerts(t *testing.T) {
	start := time.Date(2026, 11, 2, 9, 15, 0, 0, time.UTC)
	end := start.Add(25 * time.Minute)
	base := Alert{MonitorID: "m1", MonitorName: "Shop", MonitorURL: "https://shop.example.com", StartedAt: start, SlowThresholdMs: 2000, SlowPeakMs: 4350,
		AppBaseURL: "https://app.example.com", To: "me@example.com", From: "alerts@example.com"}

	slow := base
	slow.Event = EventSlow
	m := Render(slow)
	if m.Subject != "HawkEye alert: Shop is responding slowly" {
		t.Fatalf("%q", m.Subject)
	}
	for _, want := range []string{"The website is online", "2,000 ms", "4,350 ms", "2026-11-02 09:15 UTC", "https://app.example.com/monitors/m1"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("slow alert missing %q:\n%s", want, m.Text)
		}
	}
	if strings.Contains(m.Text, "offline") {
		t.Error("a slow site is not offline; the alert must not say so")
	}

	back := base
	back.Event, back.ResolvedAt = EventSlowRecovery, &end
	m = Render(back)
	if m.Subject != "HawkEye: Shop is responding normally again" || !strings.Contains(m.Text, "25m") {
		t.Fatalf("%q\n%s", m.Subject, m.Text)
	}

	c := chatContent(slow, end)
	if c.Title != "🐢 Shop is responding slowly" || c.Tone != toneWarning || c.Link != "https://app.example.com/monitors/m1" || !strings.Contains(c.Lines[0], "slowest 4,350 ms") {
		t.Fatalf("%+v", c)
	}
	if c := chatContent(back, end); c.Tone != toneGood || !strings.Contains(c.Lines[0], "after 25m") {
		t.Fatalf("%+v", c)
	}

	w := buildWebhook(back, end)
	if w.Event != "slow_recovery" || w.Slowdown == nil || w.Slowdown.ThresholdMs != 2000 || w.Slowdown.PeakMs != 4350 || w.Slowdown.ResolvedAt == nil ||
		w.Incident != nil || w.Monitor == nil || w.URL != "https://app.example.com/monitors/m1" {
		t.Fatalf("%+v", w)
	}
	if w := buildWebhook(slow, end); w.Slowdown == nil || w.Slowdown.ResolvedAt != nil {
		t.Fatalf("a slow event carries no end time: %+v", w.Slowdown)
	}
}

func TestMilliseconds(t *testing.T) {
	for ms, want := range map[int]string{0: "0 ms", 950: "950 ms", 1000: "1,000 ms", 12345: "12,345 ms", 1234567: "1,234,567 ms"} {
		if got := Milliseconds(ms); got != want {
			t.Errorf("%d: %q", ms, got)
		}
	}
}

func TestRenderHeartbeatAlerts(t *testing.T) {
	start := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	end := start.Add(25 * time.Minute)
	base := Alert{MonitorType: "heartbeat", MonitorID: "m1", MonitorName: "Nightly backup", StartedAt: start, AppBaseURL: "https://app"}

	missed := base
	missed.Event, missed.Cause = EventOutage, "No ping since 2026-11-02 02:00 UTC"
	m := Render(missed)
	if m.Subject != "HawkEye alert: Nightly backup did not check in on time" || !strings.Contains(m.Text, "none arrived in time") ||
		!strings.Contains(m.Text, "https://app/monitors/m1") || strings.Contains(m.Text, "Website") {
		t.Fatalf("missed: %s\n%s", m.Subject, m.Text)
	}
	failed := base
	failed.Event, failed.Cause = EventOutage, "The job exited with status 3"
	if m := Render(failed); m.Subject != "HawkEye alert: Nightly backup reported a failure" || !strings.Contains(m.Text, "status 3") {
		t.Fatalf("failed: %s\n%s", m.Subject, m.Text)
	}
	rec := base
	rec.Event, rec.ResolvedAt = EventRecovery, &end
	if m := Render(rec); m.Subject != "HawkEye recovery: Nightly backup is checking in again" || !strings.Contains(m.Text, "25m") {
		t.Fatalf("recovery: %s\n%s", m.Subject, m.Text)
	}

	if c := chatContent(failed, end); c.Title != "🔴 Nightly backup reported a failure" || c.Target != "" {
		t.Fatalf("chat: %+v", c)
	}
	if c := chatContent(rec, end); !strings.Contains(c.Title, "checking in again") {
		t.Fatalf("chat recovery: %+v", c)
	}
	if p := buildWebhook(missed, end); p.Monitor.Type != "heartbeat" || p.Monitor.URL != "" {
		t.Fatalf("webhook: %+v", p.Monitor)
	}
	if p := buildWebhook(Alert{Event: EventOutage, MonitorName: "Site", MonitorURL: "https://x"}, end); p.Monitor.Type != "http" {
		t.Fatalf("website webhook type: %+v", p.Monitor)
	}
}
