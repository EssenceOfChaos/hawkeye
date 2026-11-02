package badges

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var views = map[string]View{
	"up":           {State: StateUp, Percent: "99.98%", PeriodDays: 30},
	"up-100-free":  {State: StateUp, Percent: "100%", PeriodDays: 7},
	"down":         {State: StateDown, Percent: "97.41%", PeriodDays: 30},
	"paused":       {State: StateUnknown, Percent: "99.50%", PeriodDays: 30},
	"no-data":      {State: StateUp, Percent: "", PeriodDays: 30}, // a new monitor: up, but too few checks to say
	"no-data-down": {State: StateDown, PeriodDays: 7},
}

func TestEveryBadgeIsWellFormedSVG(t *testing.T) {
	for name, v := range views {
		for _, style := range []string{Large, Small} {
			svg, err := Render(style, v)
			if err != nil {
				t.Fatalf("%s/%s: %v", name, style, err)
			}
			dec := xml.NewDecoder(strings.NewReader(string(svg)))
			for {
				if _, err := dec.Token(); err != nil {
					if err.Error() != "EOF" {
						t.Fatalf("%s/%s is not well-formed XML: %v\n%s", name, style, err, svg)
					}
					break
				}
			}
			for _, banned := range []string{"<script", "href", "<image", "<foreignObject", "@import"} {
				if strings.Contains(string(svg), banned) {
					t.Fatalf("%s/%s contains %q", name, style, banned)
				}
			}
		}
	}
}

func TestBadgeText(t *testing.T) {
	cases := []struct {
		view        string
		style, want string
		icon        string
	}{
		{"up", Large, "Last 30 days", "#29D68D"},
		{"up-100-free", Large, "Last 7 days", "#29D68D"},
		{"up-100-free", Small, ">7d<", "#29D68D"},
		{"down", Large, "Service currently down", "#FF6F7A"},
		{"down", Small, "down now", "#FF6F7A"},
		{"paused", Large, "Status unknown · Last 30 days", "#F1C86A"},
		{"no-data", Large, "Not enough data yet", "#F1C86A"},
		{"no-data", Small, "No data yet", "#F1C86A"},
		{"no-data-down", Large, "Not enough data yet", "#F1C86A"},
	}
	for _, c := range cases {
		svg, _ := Render(c.style, views[c.view])
		s := string(svg)
		if !strings.Contains(s, c.want) || !strings.Contains(s, c.icon) {
			t.Errorf("%s/%s: want %q and icon %s", c.view, c.style, c.want, c.icon)
		}
	}
	svg, _ := Render(Large, views["up"])
	if !strings.Contains(string(svg), "99.98 percent uptime over the last 30 days. Monitored by HawkEye.") || !strings.Contains(string(svg), "<tspan>99.98%</tspan>") {
		t.Fatalf("%s", svg)
	}
	if svg, _ := Render(Large, views["no-data"]); strings.Contains(string(svg), "uptime</tspan>") {
		t.Fatal("no percentage, so no 'uptime' label beside the dash")
	}
}

func TestFormatPercentNeverRoundsUp(t *testing.T) {
	for in, want := range map[float64]string{100: "100%", 99.999: "99.99%", 99.98: "99.98%", 99.5: "99.50%", 0: "0.00%", 87.123: "87.12%"} {
		if got := FormatPercent(in); got != want {
			t.Errorf("%v: %q, want %q", in, got, want)
		}
	}
}

func TestWriteBadgePreview(t *testing.T) {
	dir := os.Getenv("BADGE_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set BADGE_PREVIEW_DIR to write preview files")
	}
	for name, v := range views {
		for _, style := range []string{Large, Small} {
			svg, _ := Render(style, v)
			if err := os.WriteFile(filepath.Join(dir, name+"-"+style+".svg"), svg, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
