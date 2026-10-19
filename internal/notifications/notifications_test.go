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
		"alert-recovery": {Event: EventRecovery, MonitorName: "Checkout", MonitorURL: "https://shop.example.com/checkout", StartedAt: started, ResolvedAt: &resolved, AppBaseURL: base, From: "alerts@hawkeye.observer", To: "owner@example.com"},
	} {
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(Render(a).HTML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
