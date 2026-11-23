package checks

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DNS change alerts (docs/plan-dns-change-alerts.md): each DNS monitor keeps, per check location, the set of answers it accepts as normal (the
// baseline). A different set seen in DNSChangeConfirmations checks in a row is a confirmed change at that location; the store opens a change for
// the monitor once most of its locations have confirmed one. These are the rules for one location, kept free of the database so they can be
// tested on their own.

const (
	// DNSChangeConfirmations is how many checks in a row must see a different set before it counts as a change.
	DNSChangeConfirmations = 2
	// DNSPoolWindow is how long an A or AAAA answer stays in a location's pool of known addresses (rotation within it is not a change).
	DNSPoolWindow = 7 * 24 * time.Hour
	// MaxDNSAnswers and MaxDNSAnswerChars cap what is stored, so an unusual record can't bloat the tables.
	MaxDNSAnswers     = 20
	MaxDNSAnswerChars = 1000
)

// DNSLocation is one location's change-alert state.
type DNSLocation struct {
	Baseline     []string             // the accepted set (sorted); nil before the first answer
	Pending      []string             // a different set seen at the last check, not yet confirmed
	PendingCount int                  // how many checks in a row have seen Pending
	Last         []string             // the latest answers
	Changed      bool                 // this location has a confirmed change that nobody has accepted
	Seen         map[string]time.Time // A/AAAA pool: when each address was last seen
}

// DNSStep is what folding one answer changed at a location.
type DNSStep int

const (
	DNSSame        DNSStep = iota // nothing new
	DNSInitialized                // the first answer became the baseline (never alerts)
	DNSPending                    // a different set was seen, not yet confirmed
	DNSConfirmed                  // a different set was seen DNSChangeConfirmations times in a row
	DNSBack                       // a location with a confirmed change sees its baseline again
)

// NormalizeDNSAnswers sorts answers, drops duplicates and applies the caps.
func NormalizeDNSAnswers(answers []string) []string {
	out := make([]string, 0, min(len(answers), MaxDNSAnswers))
	for _, a := range answers {
		if utf8.RuneCountInString(a) > MaxDNSAnswerChars {
			a = string([]rune(a)[:MaxDNSAnswerChars])
		}
		out = append(out, a)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > MaxDNSAnswers {
		out = out[:MaxDNSAnswers]
	}
	return out
}

// pooled reports whether a record type tolerates rotation: A and AAAA, unless the monitor is strict.
func pooled(recordType string, strict bool) bool {
	return !strict && (recordType == RecordA || recordType == RecordAAAA)
}

// FoldDNS folds one successful lookup's answers (already normalized) into a location's state. For A and AAAA (not strict) the set counts as the
// same while every address is in the location's pool: the baseline plus addresses seen in the last DNSPoolWindow. Everything else is compared
// exactly.
func FoldDNS(s DNSLocation, answers []string, recordType string, strict bool, now time.Time) (DNSLocation, DNSStep) {
	s.Last = answers
	// A copy: the caller's state is left as it was (the store only saves the result).
	s.Seen = maps.Clone(s.Seen)
	if s.Seen == nil {
		s.Seen = map[string]time.Time{}
	}
	pool := pooled(recordType, strict)
	if s.Baseline == nil {
		s.Baseline, s.Pending, s.PendingCount, s.Changed = answers, nil, 0, false
		if pool {
			for _, a := range answers {
				s.Seen[a] = now
			}
		}
		return s, DNSInitialized
	}
	// Forget addresses not seen within the window, except the baseline's.
	for a, at := range s.Seen {
		if now.Sub(at) > DNSPoolWindow && !slices.Contains(s.Baseline, a) {
			delete(s.Seen, a)
		}
	}
	same := slices.Equal(answers, s.Baseline)
	if pool && !same {
		same = !slices.ContainsFunc(answers, func(a string) bool {
			_, known := s.Seen[a]
			return !known && !slices.Contains(s.Baseline, a)
		})
	}
	if same {
		if pool {
			for _, a := range answers {
				s.Seen[a] = now
			}
		}
		s.Pending, s.PendingCount = nil, 0
		if s.Changed {
			s.Changed = false
			return s, DNSBack
		}
		return s, DNSSame
	}
	if slices.Equal(answers, s.Pending) {
		s.PendingCount++
	} else {
		s.Pending, s.PendingCount = answers, 1
	}
	if s.PendingCount >= DNSChangeConfirmations && !s.Changed {
		s.Changed = true
		return s, DNSConfirmed
	}
	if s.Changed {
		return s, DNSSame // still changed; the open change already says so
	}
	return s, DNSPending
}

// AcceptDNS makes the location's latest answers its baseline: the change was intended.
func AcceptDNS(s DNSLocation, recordType string, strict bool, now time.Time) DNSLocation {
	if s.Last != nil {
		s.Baseline = s.Last
	}
	s.Pending, s.PendingCount, s.Changed = nil, 0, false
	if pooled(recordType, strict) {
		s.Seen = maps.Clone(s.Seen)
		if s.Seen == nil {
			s.Seen = map[string]time.Time{}
		}
		for _, a := range s.Baseline {
			s.Seen[a] = now
		}
	}
	return s
}

// DescribeDNSChange says what changed in a few words, for the history list: "mail.example.com → mx.other.net", or "1 address added".
func DescribeDNSChange(before, after []string) string {
	if len(before) == 1 && len(after) == 1 {
		return before[0] + " → " + after[0]
	}
	var added, removed int
	for _, a := range after {
		if !slices.Contains(before, a) {
			added++
		}
	}
	for _, b := range before {
		if !slices.Contains(after, b) {
			removed++
		}
	}
	var parts []string
	if added > 0 {
		parts = append(parts, plural(added, "answer")+" added")
	}
	if removed > 0 {
		parts = append(parts, plural(removed, "answer")+" removed")
	}
	if len(parts) == 0 {
		return "answers reordered"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
