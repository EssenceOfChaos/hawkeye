package checks

import "testing"

func TestApplySpeedNeedsAStreakBothWays(t *testing.T) {
	const threshold, n = 1000, 3
	var s SlowState
	var tr SpeedTransition
	step := func(ms int) SpeedTransition { s, tr = ApplySpeed(s, ms, threshold, n); return tr }

	// One slow response, or two, never alerts; a quick one in between starts the count again.
	for _, ms := range []int{1500, 1500, 400, 1500, 1500} {
		if step(ms) != NoSpeedChange || s.Slow {
			t.Fatalf("after %d ms: %+v %v", ms, s, tr)
		}
	}
	if step(1001) != BecameSlow || !s.Slow {
		t.Fatalf("the third slow response in a row confirms it: %+v", s)
	}
	if step(5000) != NoSpeedChange {
		t.Fatal("an open slowdown is reported once")
	}
	// Exactly the threshold is not slow; recovery needs as long a streak.
	for _, ms := range []int{1000, 900, 2000, 900, 900} {
		if step(ms) != NoSpeedChange || !s.Slow {
			t.Fatalf("after %d ms: %+v %v", ms, s, tr)
		}
	}
	if step(10) != BackToNormal || s.Slow || s.SlowStreak != 0 {
		t.Fatalf("%+v", s)
	}
}
