package attribution

import (
	"strings"
	"testing"
	"time"
)

func TestCleanKeepsPlainLabels(t *testing.T) {
	now := time.Now()
	seen := now.Add(-48 * time.Hour)
	got, ok := Clean(Attribution{Source: " ProductHunt ", Medium: "Referral", Campaign: "Launch Nov-17 (day 1)", ClickID: "google_ads",
		ReferrerHost: "WWW.ProductHunt.com", LandingPath: "/docs/cron-job-monitoring", FirstSeenAt: &seen}, now)
	if !ok || got.Source != "producthunt" || got.Medium != "referral" || got.Campaign != "Launch Nov-17 (day 1)" || got.ClickID != "google_ads" ||
		got.ReferrerHost != "www.producthunt.com" || got.LandingPath != "/docs/cron-job-monitoring" || got.FirstSeenAt == nil {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestCleanDropsAnythingThatIsNotAShortLabel(t *testing.T) {
	now := time.Now()
	old, future := now.AddDate(-1, 0, 0), now.Add(72*time.Hour)
	for name, in := range map[string]Attribution{
		"email address":      {Source: "alice@example.com"},
		"query string":       {Source: "x?token=abc"},
		"fragment":           {Campaign: "a#b"},
		"too long":           {Campaign: strings.Repeat("a", 101)},
		"unknown click id":   {ClickID: "abc123"},
		"referrer with path": {ReferrerHost: "example.com/page"},
		"single-label host":  {ReferrerHost: "localhost"},
		"landing with query": {LandingPath: "/reset-password?token=abc"},
		"relative landing":   {LandingPath: "docs"},
		"control character":  {Medium: "c\npc"},
	} {
		if got, ok := Clean(in, now); ok {
			t.Errorf("%s: kept %+v", name, got)
		}
	}
	if got, _ := Clean(Attribution{Source: "google", FirstSeenAt: &old}, now); got.FirstSeenAt != nil {
		t.Error("a first-seen time older than the browser keeps it is dropped")
	}
	if got, _ := Clean(Attribution{Source: "google", FirstSeenAt: &future}, now); got.FirstSeenAt != nil {
		t.Error("a first-seen time in the future is dropped")
	}
	if got, ok := Clean(Attribution{Source: "google", Medium: "a@b"}, now); !ok || got.Source != "google" || got.Medium != "" {
		t.Errorf("one bad field is dropped, the rest kept: %+v", got)
	}
}
