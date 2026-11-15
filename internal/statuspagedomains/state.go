package statuspagedomains

import "time"

// States of a claimed domain (the migration's CHECK).
const (
	StatePending  = "pending"  // the records have not been seen yet; not served
	StateActive   = "active"   // verified and served
	StateFailing  = "failing"  // was active, a record is now wrong; still served while the owner fixes it
	StateDisabled = "disabled" // failed for too long; not served until it verifies again
)

// The schedule. A new claim is checked often while the owner is likely to be adding the records, then less often, then not at all (until "Check
// now"). An active domain is checked daily; a failing one hourly, and it stops being served after GracePeriod.
const (
	FastPhase       = time.Hour
	FastInterval    = time.Minute
	PendingFor      = 72 * time.Hour
	PendingInterval = 15 * time.Minute
	ActiveInterval  = 24 * time.Hour
	FailingInterval = time.Hour
	GracePeriod     = 7 * 24 * time.Hour
)

// Status is the part of a domain that verification changes.
type Status struct {
	State           string
	LastError       string
	LastCheckedAt   *time.Time
	NextCheckAt     *time.Time // nil: not checked automatically
	VerifyStartedAt time.Time
	VerifiedAt      *time.Time
	FailingSince    *time.Time
}

// Served reports whether a domain in this state is served.
func Served(state string) bool { return state == StateActive || state == StateFailing }

// Next applies one verification result at now.
func Next(cur Status, r Result, now time.Time) Status {
	n := cur
	n.LastCheckedAt = &now
	n.LastError = r.Problem
	if r.OK() {
		if cur.State != StateActive && cur.State != StateFailing {
			n.VerifiedAt = &now
		}
		n.State, n.FailingSince = StateActive, nil
		n.NextCheckAt = at(now, ActiveInterval)
		return n
	}
	switch cur.State {
	case StatePending:
		n.NextCheckAt = pendingNext(cur.VerifyStartedAt, now)
	case StateActive:
		if r.Transient {
			n.NextCheckAt = at(now, FailingInterval) // no answer says nothing about the records: look again soon, change nothing
			break
		}
		n.State, n.FailingSince = StateFailing, &now
		n.NextCheckAt = at(now, FailingInterval)
	case StateFailing:
		if !r.Transient && cur.FailingSince != nil && now.Sub(*cur.FailingSince) >= GracePeriod {
			n.State, n.FailingSince, n.NextCheckAt = StateDisabled, nil, nil
			break
		}
		n.NextCheckAt = at(now, FailingInterval)
	case StateDisabled:
		// Checked again only within an attempt "Check now" started (Restart), on the same schedule as a new claim.
		n.NextCheckAt = pendingNext(cur.VerifyStartedAt, now)
	}
	return n
}

// Restart begins a new attempt to verify a domain that is not being checked automatically (a pending claim that gave up, or a disabled domain),
// as "Check now" does, so a fix that takes a while to appear in DNS is still noticed. An active or failing domain is left as it is.
func Restart(cur Status, now time.Time) Status {
	if cur.NextCheckAt == nil && (cur.State == StatePending || cur.State == StateDisabled) {
		cur.VerifyStartedAt = now
	}
	return cur
}

func pendingNext(started, now time.Time) *time.Time {
	age := now.Sub(started)
	switch {
	case age < FastPhase:
		return at(now, FastInterval)
	case age < PendingFor:
		return at(now, PendingInterval)
	}
	return nil // gave up: the owner presses "Check now" once the records are in place
}

func at(now time.Time, d time.Duration) *time.Time {
	t := now.Add(d)
	return &t
}
