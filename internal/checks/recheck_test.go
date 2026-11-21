package checks

import (
	"testing"
	"time"
)

func TestWantRecheck(t *testing.T) {
	const delay = 15 * time.Second
	// A first failure of a healthy, read-only monitor that counts, with its next check a minute away: re-checked.
	first := recheckFacts{setting: RecheckAuto, readOnly: true, failed: true, counts: true, locationFailures: 1, untilNextCheck: time.Minute}
	if !wantRecheck(first, 2, delay) {
		t.Fatal("a first failure must be re-checked")
	}
	cases := map[string]struct {
		change    func(f *recheckFacts)
		threshold int
		delay     time.Duration
		want      bool
	}{
		"success":                         {change: func(f *recheckFacts) { f.failed = false }},
		"the re-check itself":             {change: func(f *recheckFacts) { f.fromRecheck = true }},
		"maintenance or a dropped region": {change: func(f *recheckFacts) { f.counts = false }},
		"second failure in a row":         {change: func(f *recheckFacts) { f.locationFailures = 2 }},
		"location already down":           {change: func(f *recheckFacts) { f.locationDown = true }},
		"monitor already down":            {change: func(f *recheckFacts) { f.monitorDown = true }},
		"off":                             {change: func(f *recheckFacts) { f.setting = RecheckOff }},
		"auto with a write request":       {change: func(f *recheckFacts) { f.readOnly = false }},
		"always with a write request":     {change: func(f *recheckFacts) { f.readOnly, f.setting = false, RecheckAlways }, want: true},
		"always and read-only":            {change: func(f *recheckFacts) { f.setting = RecheckAlways }, want: true},
		"next check due about as soon":    {change: func(f *recheckFacts) { f.untilNextCheck = 30 * time.Second }},
		"next check just far enough":      {change: func(f *recheckFacts) { f.untilNextCheck = 31 * time.Second }, want: true},
		"one failure already confirms":    {change: func(*recheckFacts) {}, threshold: 1},
		"re-checks off for the install":   {change: func(*recheckFacts) {}, delay: -1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := first
			c.change(&f)
			th, d := 2, delay
			if c.threshold != 0 {
				th = c.threshold
			}
			if c.delay != 0 {
				d = 0
			}
			if got := wantRecheck(f, th, d); got != c.want {
				t.Fatalf("wantRecheck = %v, want %v", got, c.want)
			}
		})
	}
}

func TestWriteMethod(t *testing.T) {
	for m, want := range map[string]bool{"GET": false, "head": false, "OPTIONS": false, "POST": true, " put ": true, "patch": true, "DELETE": true} {
		if WriteMethod(m) != want {
			t.Errorf("WriteMethod(%q) = %v", m, !want)
		}
	}
}
