package notifications

import (
	"strconv"
	"strings"

	"hawkeye/internal/checks"
	"hawkeye/internal/email"
)

// AlertSnapshot is what an outage alert says HawkEye saw (docs/plan-failure-snapshots.md). It never holds the body excerpt: that stays in the
// app, because alerts are kept by third parties (mail providers, Slack, Discord) out of the organization's control.
type AlertSnapshot struct {
	Facts     checks.ResponseFacts
	StepName  string // the failing step of a multi-step check
	Changes   []checks.Change
	Locations []checks.LocationState
}

// AlertAnalysis is the AI analysis a recovery alert repeats (internal/incidentanalysis): written by a model from the snapshot, so labeled as
// possibly wrong wherever it is shown.
type AlertAnalysis struct {
	Category, LikelyCause, NextStep, Confidence string
}

const (
	maxEmailHeaders = 10
	maxChatChanges  = 2
	maxChatLine     = 240
)

// timingLine is "DNS 12 ms · connect 31 ms · TLS 48 ms · server wait 2,410 ms", leaving out phases that took no time (TLS on http://).
func timingLine(t *checks.Timings) string {
	if t == nil {
		return ""
	}
	parts := []string{"DNS " + Milliseconds(t.DNSMs), "connect " + Milliseconds(t.ConnectMs)}
	if t.TLSMs > 0 {
		parts = append(parts, "TLS "+Milliseconds(t.TLSMs))
	}
	parts = append(parts, "server wait "+Milliseconds(t.WaitMs))
	if t.Redirects > 0 {
		parts = append(parts, strconv.Itoa(t.Redirects)+" redirect"+map[bool]string{true: "s"}[t.Redirects > 1])
	}
	return strings.Join(parts, " · ")
}

// locationLine is "Amsterdam: Received HTTP 502 · Los Angeles: Received HTTP 502 · New York: OK".
func locationLine(ls []checks.LocationState) string {
	parts := make([]string, 0, len(ls))
	for _, l := range ls {
		name := l.Name
		if name == "" {
			name = l.Region
		}
		v := "OK"
		switch {
		case l.Status == checks.StatusDown && l.Error != "":
			v = l.Error
		case l.Status == checks.StatusDown:
			v = "failing"
		case l.Status != checks.StatusUp:
			v = "no result yet"
		}
		parts = append(parts, name+": "+v)
	}
	return strings.Join(parts, " · ")
}

// snapshotFacts are the email facts of a snapshot, after the alert's own.
func snapshotFacts(s *AlertSnapshot) []email.Fact {
	var out []email.Fact
	if s.StepName != "" {
		out = append(out, email.Fact{Label: "Failed step", Value: clean(s.StepName)})
	}
	f := s.Facts
	if f.StatusCode != 0 {
		v := "HTTP " + strconv.Itoa(f.StatusCode)
		if f.ContentType != "" {
			v += ", " + clean(f.ContentType)
		}
		switch {
		case f.SizeBytes >= 0 && f.SizeAtLeast:
			v += ", at least " + checks.Bytes(f.SizeBytes)
		case f.SizeBytes >= 0:
			v += ", " + checks.Bytes(f.SizeBytes)
		}
		out = append(out, email.Fact{Label: "Response", Value: v})
	}
	if f.FinalURL != "" {
		out = append(out, email.Fact{Label: "Ended at", Value: clean(f.FinalURL)})
	}
	if f.ServerAddr != "" {
		out = append(out, email.Fact{Label: "Server address", Value: f.ServerAddr})
	}
	if t := timingLine(f.Timings); t != "" {
		out = append(out, email.Fact{Label: "Timings", Value: t})
	}
	if len(s.Locations) > 0 {
		out = append(out, email.Fact{Label: "Locations", Value: locationLine(s.Locations)})
	}
	return out
}

// snapshotQuote is the email's boxed text: what changed since the last good check, then the response headers. Header values come from the
// monitored site, which is why they go in the quote (shown as plain, escaped text) rather than in the message's own wording.
func snapshotQuote(s *AlertSnapshot) string {
	var b strings.Builder
	if len(s.Changes) > 0 {
		b.WriteString("Changed since the last good check:\n")
		for _, c := range s.Changes {
			b.WriteString("• " + clean(c.String()) + "\n")
		}
	}
	if hs := s.Facts.Headers; len(hs) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Response headers:\n")
		for i, h := range hs {
			if i == maxEmailHeaders {
				b.WriteString("… and " + strconv.Itoa(len(hs)-maxEmailHeaders) + " more in HawkEye\n")
				break
			}
			b.WriteString(clean(h.Name+": "+h.Value) + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// snapshotChatLines are at most three extra lines for chat apps (and PagerDuty's details), each at most maxChatLine characters, so every app's
// size limit holds however much the snapshot says.
func snapshotChatLines(s *AlertSnapshot) []string {
	var out []string
	line := func(v string) {
		if v == "" {
			return
		}
		if r := []rune(v); len(r) > maxChatLine {
			v = string(r[:maxChatLine-1]) + "…"
		}
		out = append(out, v)
	}
	if s.StepName != "" {
		line("Failed step: " + clean(s.StepName))
	}
	if t := timingLine(s.Facts.Timings); t != "" {
		line(t)
	}
	if n := min(len(s.Changes), maxChatChanges); n > 0 {
		parts := make([]string, n)
		for i := range n {
			parts[i] = clean(s.Changes[i].String())
		}
		line("Changed: " + strings.Join(parts, " · "))
	}
	if len(s.Locations) > 0 {
		line(locationLine(s.Locations))
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// cutLine keeps a chat line within maxChatLine characters.
func cutLine(v string) string {
	if r := []rune(v); len(r) > maxChatLine {
		return string(r[:maxChatLine-1]) + "…"
	}
	return v
}
