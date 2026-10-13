package reports

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func tp(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func TestPercentTruncatesAndRequiresEnoughData(t *testing.T) {
	cases := []struct {
		ok, total int
		want      float64
		insuff    bool
	}{
		{0, 0, 0, true}, {4, 4, 0, true}, {5, 5, 100, false}, {0, 5, 0, false},
		{9999, 10000, 99.99, false},
		{2999, 3000, 99.966, false},    // 99.9666... must truncate, not round to 99.967
		{99999, 100000, 99.999, false}, // one failure in 100k must never display as 100
		{1, 3 + 2, 20, false},
	}
	for _, c := range cases {
		got, insuff := Percent(c.ok, c.total)
		if insuff != c.insuff || (!insuff && *got != c.want) {
			t.Errorf("%d/%d: got %v insufficient=%v, want %v %v", c.ok, c.total, got, insuff, c.want, c.insuff)
		}
		if insuff && got != nil {
			t.Errorf("%d/%d: a percentage must not be given with insufficient data", c.ok, c.total)
		}
		if !insuff && c.ok < c.total && *got >= 100 {
			t.Errorf("%d/%d displays as 100%% despite failures", c.ok, c.total)
		}
	}
}

func TestResolveRangePresetsAndDefault(t *testing.T) {
	r, err := ResolveRange(now, "", nil, nil, 90)
	if err != nil || r.Preset != "24h" || !r.To.Equal(now) || !r.From.Equal(now.Add(-24*time.Hour)) || r.Clamped {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = ResolveRange(now, "30d", nil, nil, 90)
	if err != nil || !r.From.Equal(now.Add(-30*24*time.Hour)) {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := ResolveRange(now, "2w", nil, nil, 90); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestResolveRangeClampsToPlanRetention(t *testing.T) {
	r, err := ResolveRange(now, "30d", nil, nil, 7)
	if err != nil || !r.Clamped || !r.From.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("free plan must clamp 30d to 7d and say so: %+v %v", r, err)
	}
	r, _ = ResolveRange(now, "7d", nil, nil, 7)
	if r.Clamped {
		t.Fatal("a range exactly within retention is not clamped")
	}
}

func TestResolveRangeCustom(t *testing.T) {
	r, err := ResolveRange(now, "", tp("2026-10-10T00:00:00Z"), tp("2026-10-11T00:00:00Z"), 90)
	if err != nil || r.Preset != "" || r.Clamped {
		t.Fatalf("%+v %v", r, err)
	}
	r, _ = ResolveRange(now, "", tp("2026-10-10T00:00:00Z"), tp("2030-01-01T00:00:00Z"), 90)
	if !r.To.Equal(now) {
		t.Fatalf("future end must be capped at now: %v", r.To)
	}
	for name, c := range map[string]struct {
		preset   string
		from, to *time.Time
		msg      string
	}{
		"only from":        {"", tp("2026-10-10T00:00:00Z"), nil, "together"},
		"preset and range": {"7d", tp("2026-10-10T00:00:00Z"), tp("2026-10-11T00:00:00Z"), "not both"},
		"reversed":         {"", tp("2026-10-11T00:00:00Z"), tp("2026-10-10T00:00:00Z"), "before"},
		"empty":            {"", tp("2026-10-11T00:00:00Z"), tp("2026-10-11T00:00:00Z"), "before"},
		"too long":         {"", tp("2024-01-01T00:00:00Z"), tp("2026-01-01T00:00:00Z"), "366"},
		"entirely too old": {"", tp("2026-01-01T00:00:00Z"), tp("2026-01-02T00:00:00Z"), "older"},
	} {
		if _, err := ResolveRange(now, c.preset, c.from, c.to, 7); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: want error containing %q, got %v", name, c.msg, err)
		}
	}
}

func TestBucketSecondsBoundsPointCount(t *testing.T) {
	for _, d := range []time.Duration{time.Minute, time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 90 * 24 * time.Hour, 365 * 24 * time.Hour, MaxWindow} {
		b := BucketSeconds(d)
		if points := int(d/time.Second)/b + 1; points > MaxPoints+1 {
			t.Errorf("%v: bucket %ds gives %d points", d, b, points)
		}
	}
	if BucketSeconds(24*time.Hour) != 900 || BucketSeconds(time.Hour) != 60 {
		t.Fatalf("unexpected buckets: %d %d", BucketSeconds(24*time.Hour), BucketSeconds(time.Hour))
	}
}

func TestCutoff(t *testing.T) {
	if got := Cutoff(now, 7); !got.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatal(got)
	}
}
