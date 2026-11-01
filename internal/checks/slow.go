package checks

// SlowState is the part of a monitor's (or one location's) row that response times change.
type SlowState struct {
	Slow         bool
	SlowStreak   int // consecutive successful checks above the threshold
	NormalStreak int // consecutive successful checks at or under it
}

// SpeedTransition is a confirmed change of speed.
type SpeedTransition int

const (
	NoSpeedChange SpeedTransition = iota
	BecameSlow                    // responses stayed above the threshold for the required number of checks
	BackToNormal                  // and then stayed at or under it as long
)

// ApplySpeed folds one successful check's response time into the state. A monitor becomes slow only after `checks` slow responses in a row, and
// normal again only after as many normal ones, so one slow (or one quick) response never changes anything. Failed checks are not passed in:
// an outage is reported on its own and says nothing about speed.
func ApplySpeed(s SlowState, responseMs, thresholdMs, checks int) (SlowState, SpeedTransition) {
	checks = max(checks, 1)
	if responseMs > thresholdMs {
		s.NormalStreak = 0
		s.SlowStreak = min(s.SlowStreak+1, counterCap)
		if !s.Slow && s.SlowStreak >= checks {
			s.Slow = true
			return s, BecameSlow
		}
		return s, NoSpeedChange
	}
	s.SlowStreak = 0
	s.NormalStreak = min(s.NormalStreak+1, counterCap)
	if s.Slow && s.NormalStreak >= checks {
		s.Slow = false
		return s, BackToNormal
	}
	return s, NoSpeedChange
}
