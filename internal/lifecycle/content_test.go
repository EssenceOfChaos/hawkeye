package lifecycle

import (
	"strings"
	"testing"
)

func TestEveryRuleHasAnEmailThatRendersCompletely(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rules {
		if seen[r.Kind] {
			t.Fatalf("kind %s is used twice", r.Kind)
		}
		seen[r.Kind] = true
		c := Build(r.Kind, "https://app.test", "", "tok-123")
		if c.Heading == "" || len(c.Paragraphs) == 0 && len(c.Facts) == 0 || c.ButtonLabel == "" || c.Preheader == "" {
			t.Errorf("%s is incomplete: %+v", r.Kind, c)
		}
		m := c.Message("HawkEye <hi@app.test>", "u@example.com", r.Subject)
		if !strings.Contains(m.Text, "https://app.test/unsubscribe?token=tok-123") || !strings.Contains(m.HTML, "https://app.test/unsubscribe?token=tok-123") {
			t.Errorf("%s has no unsubscribe link in the footer", r.Kind)
		}
		if m.Unsubscribe != "https://app.test/api/v1/lifecycle/unsubscribe?token=tok-123" {
			t.Errorf("%s one-click URL = %q", r.Kind, m.Unsubscribe)
		}
		if !strings.Contains(c.ButtonURL, "/monitors/new?") || !strings.Contains(c.ButtonURL, "utm_campaign="+r.Kind) {
			t.Errorf("%s button = %q", r.Kind, c.ButtonURL)
		}
		if strings.Contains(m.HTML, "ZgotmplZ") || m.HTML == "" {
			t.Errorf("%s html did not render", r.Kind)
		}
	}
}

func TestRulesAreOrderedAndTheirWindowsDoNotOverlap(t *testing.T) {
	for i, r := range Rules {
		if r.After <= 0 || r.Until <= r.After {
			t.Errorf("%s window is empty: %v..%v", r.Kind, r.After, r.Until)
		}
		if i > 0 && r.After < Rules[i-1].Until {
			t.Errorf("%s starts before %s ends, so one person could be due both at once", r.Kind, Rules[i-1].Kind)
		}
	}
}

func TestAnUnknownKindBuildsNoCopyRatherThanGuessing(t *testing.T) {
	if c := Build("nope", "https://app.test", "", "t"); c.Heading != "" || len(c.Paragraphs) != 0 {
		t.Fatalf("%+v", c)
	}
}
