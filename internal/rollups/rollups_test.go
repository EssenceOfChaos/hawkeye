package rollups

import (
	"testing"
	"time"
)

func TestSpanCoversWholeHoursInsideTheRangeUpToThrough(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name              string
		from, to, through string
		wantA, wantB      string
	}{
		{"odd edges, totals complete", "2026-11-10T08:17:00Z", "2026-11-12T14:43:00Z", "2026-11-13T00:00:00Z", "2026-11-10T09:00:00Z", "2026-11-12T14:00:00Z"},
		{"totals end inside the range", "2026-11-10T08:17:00Z", "2026-11-12T14:43:00Z", "2026-11-11T05:00:00Z", "2026-11-10T09:00:00Z", "2026-11-11T05:00:00Z"},
		{"no totals yet", "2026-11-10T08:17:00Z", "2026-11-12T14:43:00Z", "", "2026-11-10T09:00:00Z", "2026-11-10T09:00:00Z"},
		{"totals end before the range", "2026-11-10T08:17:00Z", "2026-11-12T14:43:00Z", "2026-11-01T00:00:00Z", "2026-11-10T09:00:00Z", "2026-11-10T09:00:00Z"},
		{"inside one hour", "2026-11-10T08:17:00Z", "2026-11-10T08:43:00Z", "2026-11-13T00:00:00Z", "2026-11-10T08:43:00Z", "2026-11-10T08:43:00Z"},
		{"exact hours", "2026-11-10T08:00:00Z", "2026-11-10T11:00:00Z", "2026-11-13T00:00:00Z", "2026-11-10T08:00:00Z", "2026-11-10T11:00:00Z"},
	}
	for _, c := range cases {
		var through time.Time
		if c.through != "" {
			through = at(c.through)
		}
		from, to := at(c.from), at(c.to)
		a, b := Span(from, to, through)
		if !a.Equal(at(c.wantA)) || !b.Equal(at(c.wantB)) {
			t.Errorf("%s: got [%s, %s), want [%s, %s)", c.name, a, b, c.wantA, c.wantB)
		}
		if a.Before(from) || b.After(to) || b.Before(a) {
			t.Errorf("%s: [%s, %s) is not inside [%s, %s)", c.name, a, b, from, to)
		}
	}
}

func TestSpanWorksInAnyTimeZone(t *testing.T) {
	india := time.FixedZone("IST", 5*3600+1800)         // half-hour offset: hours must still be UTC hours
	from := time.Date(2026, 11, 10, 14, 0, 0, 0, india) // 08:30 UTC
	a, _ := Span(from, from.Add(5*time.Hour), time.Time{}.Add(1<<62))
	if want := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC); !a.Equal(want) {
		t.Fatalf("a = %s, want %s", a, want)
	}
}
