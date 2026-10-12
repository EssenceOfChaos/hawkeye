package checks

import "testing"

var th = Thresholds{Failure: 2, Recovery: 2}

func run(s State, outcomes ...bool) (State, []Transition) {
	var ts []Transition
	for _, ok := range outcomes {
		var t Transition
		s, t = Apply(s, ok, th)
		ts = append(ts, t)
	}
	return s, ts
}

func TestFirstSuccessMakesUnknownUpWithoutEvent(t *testing.T) {
	s, ts := run(State{Status: StatusUnknown}, true)
	if s.Status != StatusUp || ts[0] != NoTransition {
		t.Fatalf("%+v %v", s, ts)
	}
}

func TestSingleFailureDoesNotTakeMonitorDown(t *testing.T) {
	s, ts := run(State{Status: StatusUp, Successes: 5}, false)
	if s.Status != StatusUp || s.Failures != 1 || s.Successes != 0 || ts[0] != NoTransition {
		t.Fatalf("%+v %v", s, ts)
	}
}

func TestConfirmedOutageFiresOnce(t *testing.T) {
	s, ts := run(State{Status: StatusUp}, false, false, false, false)
	if s.Status != StatusDown {
		t.Fatalf("%+v", s)
	}
	want := []Transition{NoTransition, WentDown, NoTransition, NoTransition}
	for i := range want {
		if ts[i] != want[i] {
			t.Fatalf("transitions %v, want %v", ts, want)
		}
	}
}

func TestUnknownMonitorWithFailuresGoesDown(t *testing.T) {
	s, ts := run(State{Status: StatusUnknown}, false, false)
	if s.Status != StatusDown || ts[1] != WentDown {
		t.Fatalf("%+v %v", s, ts)
	}
}

func TestRecoveryRequiresConsecutiveSuccesses(t *testing.T) {
	s, ts := run(State{Status: StatusDown, Failures: 4}, true, false, true, true)
	// success, fail (resets), success, success -> recovered on the 4th result only
	if s.Status != StatusUp || ts[3] != Recovered || ts[0] != NoTransition || ts[2] != NoTransition {
		t.Fatalf("%+v %v", s, ts)
	}
}

func TestFlappingNeverConfirmsOutage(t *testing.T) {
	s, ts := run(State{Status: StatusUp}, false, true, false, true, false, true)
	if s.Status != StatusUp {
		t.Fatalf("%+v", s)
	}
	for _, tr := range ts {
		if tr != NoTransition {
			t.Fatalf("unexpected transition in %v", ts)
		}
	}
}

func TestThresholdsAreConfigurableAndSanitised(t *testing.T) {
	s, ts := Apply(State{Status: StatusUp}, false, Thresholds{Failure: 1, Recovery: 1})
	if s.Status != StatusDown || ts != WentDown {
		t.Fatal("threshold 1 should confirm immediately")
	}
	s, ts = Apply(State{Status: StatusDown}, true, Thresholds{Failure: 1, Recovery: 1})
	if s.Status != StatusUp || ts != Recovered {
		t.Fatal("recovery threshold 1")
	}
	// Invalid thresholds fall back to the defaults instead of disabling confirmation.
	if s, _ := Apply(State{Status: StatusUp}, false, Thresholds{}); s.Status != StatusUp {
		t.Fatal("zero thresholds must not make a single failure fatal")
	}
}

func TestCountersAreCapped(t *testing.T) {
	s, _ := Apply(State{Status: StatusDown, Failures: counterCap}, false, th)
	if s.Failures != counterCap {
		t.Fatalf("%d", s.Failures)
	}
}
