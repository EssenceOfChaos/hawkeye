package notifications

import (
	"testing"
	"time"
)

func quiet(t *testing.T, start, end string, tz string) *QuietHours {
	t.Helper()
	clock := func(v string) int {
		c, err := time.Parse("15:04", v)
		if err != nil {
			t.Fatal(err)
		}
		return c.Hour()*60 + c.Minute()
	}
	s, e := clock(start), clock(end)
	q, err := NewQuietHours(&s, &e, &tz)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestQuietHoursUntil(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, ny) }
	overnight := quiet(t, "22:00", "07:00", "America/New_York")
	daytime := quiet(t, "12:00", "13:30", "America/New_York")

	for name, c := range map[string]struct {
		q       *QuietHours
		t, want time.Time
	}{
		"overnight, before midnight":  {overnight, at(2026, 11, 2, 23, 15), at(2026, 11, 3, 7, 0)},
		"overnight, after midnight":   {overnight, at(2026, 11, 3, 2, 0), at(2026, 11, 3, 7, 0)},
		"overnight, at the start":     {overnight, at(2026, 11, 2, 22, 0), at(2026, 11, 3, 7, 0)},
		"overnight, at the end: free": {overnight, at(2026, 11, 3, 7, 0), at(2026, 11, 3, 7, 0)},
		"overnight, daytime: free":    {overnight, at(2026, 11, 3, 15, 0), at(2026, 11, 3, 15, 0)},
		"same day, inside":            {daytime, at(2026, 11, 3, 12, 45), at(2026, 11, 3, 13, 30)},
		"same day, before: free":      {daytime, at(2026, 11, 3, 11, 59), at(2026, 11, 3, 11, 59)},
		"no quiet hours":              {nil, at(2026, 11, 3, 2, 0), at(2026, 11, 3, 2, 0)},
		// Daylight saving ends in New York on 2026-11-01: the night is an hour longer, and the window still ends at 07:00 local time.
		"overnight across the DST change": {overnight, at(2026, 10, 31, 23, 0), at(2026, 11, 1, 7, 0)},
	} {
		if got := c.q.Until(c.t); !got.Equal(c.want) {
			t.Errorf("%s: Until(%s) = %s, want %s", name, c.t, got, c.want)
		}
	}

	// The window is evaluated in its own zone, whatever zone the time is given in.
	if got := overnight.Until(at(2026, 11, 3, 2, 0).UTC()); !got.Equal(at(2026, 11, 3, 7, 0)) {
		t.Errorf("UTC input: %s", got)
	}
}

func TestOutageTimingCombinesDelayAndQuietHours(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	started := time.Date(2026, 11, 3, 21, 50, 0, 0, ny)
	confirmed := started.Add(2 * time.Minute)

	if got := (Timing{}).OutageAt(confirmed, started); !got.Equal(confirmed) {
		t.Errorf("no delay: %s", got)
	}
	if got := (Timing{DelayMinutes: 5}).OutageAt(confirmed, started); !got.Equal(started.Add(5 * time.Minute)) {
		t.Errorf("5-minute delay counts from the first failed check: %s", got)
	}
	// A delay that has already passed when the outage is confirmed holds nothing.
	if got := (Timing{DelayMinutes: 1}).OutageAt(confirmed, started); !got.Equal(confirmed) {
		t.Errorf("elapsed delay: %s", got)
	}
	// 21:50 + 15 minutes lands inside 22:00-07:00, so the alert waits for the morning.
	q := quiet(t, "22:00", "07:00", "America/New_York")
	if got := (Timing{DelayMinutes: 15, Quiet: q}).OutageAt(confirmed, started); !got.Equal(time.Date(2026, 11, 4, 7, 0, 0, 0, ny)) {
		t.Errorf("delay into quiet hours: %s", got)
	}
	if got := (Timing{Quiet: q}).At(time.Date(2026, 11, 3, 18, 0, 0, 0, ny)); !got.Equal(time.Date(2026, 11, 3, 18, 0, 0, 0, ny)) {
		t.Errorf("outside quiet hours: %s", got)
	}
}

func TestNewQuietHours(t *testing.T) {
	if q, err := NewQuietHours(nil, nil, nil); q != nil || err != nil {
		t.Fatal("unset quiet hours must be nil")
	}
	s, e, bad := 0, 60, "Mars/Olympus_Mons"
	if _, err := NewQuietHours(&s, &e, &bad); err == nil {
		t.Fatal("an unknown zone must be an error")
	}
}

func TestHoldIsOnlyForTheFuture(t *testing.T) {
	if hold(time.Now().Add(-time.Second)) != nil || hold(time.Now()) != nil {
		t.Fatal("a time that has come is stored as NULL, so the alert is not treated as held")
	}
	if h := hold(time.Now().Add(time.Hour)); h == nil {
		t.Fatal("a future time is a hold")
	}
}
