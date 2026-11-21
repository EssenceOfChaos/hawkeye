package checks

import (
	"strings"
	"time"
)

// Re-check settings of a monitor (monitors.recheck): when a healthy monitor fails, the same location checks it again shortly after instead of
// waiting a whole interval for the confirming check (docs/plan-immediate-recheck.md).
const (
	RecheckAuto   = "auto"   // re-check only when every request is read-only, so nothing that may change state is sent again unasked
	RecheckAlways = "always" // re-check whatever the request methods
	RecheckOff    = "off"    // never re-check
)

// WriteMethod reports whether an HTTP method may change state on the target (POST, PUT, PATCH, DELETE). GET, HEAD and OPTIONS are read-only.
func WriteMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// writeMethodsSQL is WriteMethod's list for queries.
const writeMethodsSQL = `('POST', 'PUT', 'PATCH', 'DELETE')`

// recheckFacts is what deciding on a re-check needs to know about a result and its monitor.
type recheckFacts struct {
	setting          string        // RecheckAuto, RecheckAlways or RecheckOff
	readOnly         bool          // every request the check sends is read-only (always true for DNS lookups)
	failed           bool          // the result is a failure
	fromRecheck      bool          // the result is itself a re-check
	counts           bool          // the result changes the monitor's health (not in maintenance, from a current location)
	locationFailures int           // the reporting location's failure streak, including this result
	locationDown     bool          // the reporting location is down
	monitorDown      bool          // the monitor is down (before this result)
	untilNextCheck   time.Duration // until the monitor's next scheduled check
}

// wantRecheck decides whether a result should be re-checked: a first failure of a healthy location on a healthy monitor, when the next
// scheduled check isn't about as soon as a re-check would be. That is at most one re-check per failure streak, and none while a monitor is
// down or recovering, so a site that is really down is checked at its normal rate.
func wantRecheck(f recheckFacts, failureThreshold int, delay time.Duration) bool {
	switch {
	case delay <= 0, failureThreshold < 2:
		return false // re-checks are off for this installation, or one failure already confirms an outage
	case f.setting == RecheckOff, f.setting != RecheckAlways && !f.readOnly:
		return false
	case !f.failed, f.fromRecheck, !f.counts:
		return false
	case f.locationFailures != 1, f.locationDown, f.monitorDown:
		return false
	}
	return f.untilNextCheck > 2*delay
}
