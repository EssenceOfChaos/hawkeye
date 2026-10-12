// Package checks performs HTTP checks and turns their outcomes into monitor state.
package checks

// Monitor health statuses.
const (
	StatusUnknown = "unknown"
	StatusUp      = "up"
	StatusDown    = "down"
)

// Thresholds are the confirmation counts that guard against transient failures.
type Thresholds struct {
	Failure  int // consecutive failures before a monitor is considered down
	Recovery int // consecutive successes before a down monitor is considered up again
}

func (t Thresholds) normalized() Thresholds {
	if t.Failure < 1 {
		t.Failure = 2
	}
	if t.Recovery < 1 {
		t.Recovery = 2
	}
	return t
}

// State is the part of a monitor's row that check results change.
type State struct {
	Status    string
	Failures  int
	Successes int
}

// Transition is a confirmed change of health. Phase 5 opens and resolves incidents on these.
type Transition int

const (
	NoTransition Transition = iota
	WentDown                // confirmed outage
	Recovered               // confirmed recovery from an outage
)

const counterCap = 1 << 20

// Apply folds one check outcome into the state.
//
//	success: consecutive_failures = 0, consecutive_successes++
//	  unknown -> up (first healthy result; no event, there was no outage)
//	  down    -> up once Recovery successes in a row (Recovered)
//	failure: consecutive_successes = 0, consecutive_failures++
//	  unknown/up -> down once Failure failures in a row (WentDown)
//
// A single failure therefore never changes an "up" monitor, and one success never clears an outage.
func Apply(s State, success bool, th Thresholds) (State, Transition) {
	th = th.normalized()
	if success {
		s.Failures = 0
		s.Successes = min(s.Successes+1, counterCap)
		switch s.Status {
		case StatusDown:
			if s.Successes >= th.Recovery {
				s.Status = StatusUp
				return s, Recovered
			}
		case StatusUnknown, "":
			s.Status = StatusUp
		}
		return s, NoTransition
	}
	s.Successes = 0
	s.Failures = min(s.Failures+1, counterCap)
	if s.Status != StatusDown && s.Failures >= th.Failure {
		s.Status = StatusDown
		return s, WentDown
	}
	return s, NoTransition
}
