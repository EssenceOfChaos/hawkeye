package checks

import (
	"strings"
	"testing"
	"time"
)

func fold(t *testing.T, s DNSLocation, answers []string, rt string, strict bool, at time.Time) (DNSLocation, DNSStep) {
	t.Helper()
	return FoldDNS(s, NormalizeDNSAnswers(answers), rt, strict, at)
}

func TestTheFirstAnswerBecomesTheBaselineSilently(t *testing.T) {
	s, step := fold(t, DNSLocation{}, []string{"mx2.example.com", "mx1.example.com"}, RecordMX, false, time.Now())
	if step != DNSInitialized || strings.Join(s.Baseline, ",") != "mx1.example.com,mx2.example.com" {
		t.Fatalf("%v %v", step, s.Baseline)
	}
}

func TestAChangeNeedsTwoChecksInARow(t *testing.T) {
	now := time.Now()
	s, _ := fold(t, DNSLocation{}, []string{"mail.example.com"}, RecordMX, false, now)
	// Order and duplicates don't matter.
	if _, step := fold(t, s, []string{"mail.example.com", "mail.example.com"}, RecordMX, false, now); step != DNSSame {
		t.Fatalf("same set: %v", step)
	}
	// A one-off odd answer is forgotten.
	s, step := fold(t, s, []string{"mx.other.net"}, RecordMX, false, now)
	if step != DNSPending {
		t.Fatalf("first sighting: %v", step)
	}
	s, step = fold(t, s, []string{"mail.example.com"}, RecordMX, false, now)
	if step != DNSSame || s.Pending != nil {
		t.Fatalf("back before confirming: %v %v", step, s.Pending)
	}
	// Two in a row confirm it, once.
	s, _ = fold(t, s, []string{"mx.other.net"}, RecordMX, false, now)
	s, step = fold(t, s, []string{"mx.other.net"}, RecordMX, false, now)
	if step != DNSConfirmed || !s.Changed {
		t.Fatalf("confirmed: %v", step)
	}
	if _, step = fold(t, s, []string{"mx.other.net"}, RecordMX, false, now); step != DNSSame {
		t.Fatalf("a confirmed change isn't confirmed again: %v", step)
	}
	// Going back to the baseline is reported, so the open change can close as reverted.
	s, step = fold(t, s, []string{"mail.example.com"}, RecordMX, false, now)
	if step != DNSBack || s.Changed {
		t.Fatalf("back: %v", step)
	}
}

func TestAddressesRotatingWithinThePoolAreNotAChange(t *testing.T) {
	now := time.Now()
	s, _ := fold(t, DNSLocation{}, []string{"203.0.113.1", "203.0.113.2"}, RecordA, false, now)
	// The CDN hands out another address; once seen twice in a row it's a change (the pool only grows from answers that were the same).
	s, _ = fold(t, s, []string{"203.0.113.3"}, RecordA, false, now)
	s, step := fold(t, s, []string{"203.0.113.3"}, RecordA, false, now)
	if step != DNSConfirmed {
		t.Fatalf("a never-seen address: %v", step)
	}
	s = AcceptDNS(s, RecordA, false, now)
	// After accepting, every address seen so far is known: rotating among them is fine.
	for _, set := range [][]string{{"203.0.113.1"}, {"203.0.113.2", "203.0.113.3"}, {"203.0.113.1", "203.0.113.3"}} {
		if _, step := fold(t, s, set, RecordA, false, now.Add(time.Hour)); step != DNSSame {
			t.Fatalf("%v within the pool: %v", set, step)
		}
	}
	// An address not seen for longer than the window leaves the pool (unless it's in the baseline).
	later := now.Add(DNSPoolWindow + time.Hour)
	s.Baseline = []string{"203.0.113.3"}
	if _, step := fold(t, s, []string{"203.0.113.1"}, RecordA, false, later); step != DNSPending {
		t.Fatalf("an address gone from the pool: %v", step)
	}
	// Strict monitors compare exactly.
	if _, step := fold(t, s, []string{"203.0.113.3", "203.0.113.1"}, RecordA, true, now); step != DNSPending {
		t.Fatalf("strict: %v", step)
	}
}

func TestNormalizeDNSAnswersCaps(t *testing.T) {
	var many []string
	for i := range 30 {
		many = append(many, string(rune('a'+i%26))+string(rune('a'+i/26))+strings.Repeat("x", 1200))
	}
	got := NormalizeDNSAnswers(many)
	if len(got) != MaxDNSAnswers || len([]rune(got[0])) != MaxDNSAnswerChars {
		t.Fatalf("%d answers, first %d characters", len(got), len([]rune(got[0])))
	}
}

func TestDescribeDNSChange(t *testing.T) {
	for _, c := range []struct {
		before, after []string
		want          string
	}{
		{[]string{"mail.example.com"}, []string{"mx.other.net"}, "mail.example.com → mx.other.net"},
		{[]string{"a", "b"}, []string{"a", "b", "c"}, "1 answer added"},
		{[]string{"a", "b"}, []string{"c"}, "1 answer added, 2 answers removed"},
	} {
		if got := DescribeDNSChange(c.before, c.after); got != c.want {
			t.Errorf("%v → %v: %q, want %q", c.before, c.after, got, c.want)
		}
	}
}
