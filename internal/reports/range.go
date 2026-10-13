// Package reports computes uptime, response-time series and check history for bounded time ranges.
package reports

import (
	"time"

	"hawkeye/internal/validation"
)

// MaxWindow bounds any single request so dashboards never scan unbounded history.
const MaxWindow = 366 * 24 * time.Hour

// Presets maps range names to durations.
var Presets = map[string]time.Duration{
	"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour, "365d": 365 * 24 * time.Hour,
}

// Range is the resolved, half-open window [From, To) a report covers.
type Range struct {
	From, To      time.Time
	Preset        string // "" for a custom range
	Clamped       bool   // From was moved forward because the plan keeps less history than requested
	RetentionDays int
}

// ResolveRange turns query parameters into a validated window.
//   - preset (default "24h") or an explicit from/to pair, never both;
//   - the window may not exceed MaxWindow and `to` is capped at now;
//   - history older than the plan's retention does not exist, so From is clamped to the retention cutoff and
//     Clamped is set so the UI can say so instead of silently showing a shorter period.
func ResolveRange(now time.Time, preset string, from, to *time.Time, retentionDays int) (Range, error) {
	now = now.UTC()
	r := Range{RetentionDays: retentionDays}
	switch {
	case from != nil || to != nil:
		if from == nil || to == nil {
			return r, validation.New("from", "from and to must be given together")
		}
		if preset != "" {
			return r, validation.New("range", "use either range or from/to, not both")
		}
		r.From, r.To = from.UTC(), to.UTC()
	default:
		if preset == "" {
			preset = "24h"
		}
		d, ok := Presets[preset]
		if !ok {
			return r, validation.New("range", "must be one of 24h, 7d, 30d, 90d, 365d")
		}
		r.Preset, r.To, r.From = preset, now, now.Add(-d)
	}
	if r.To.After(now) {
		r.To = now
	}
	if !r.From.Before(r.To) {
		return r, validation.New("from", "must be before to")
	}
	if r.To.Sub(r.From) > MaxWindow {
		return r, validation.New("from", "the range may not exceed 366 days")
	}
	if cutoff := Cutoff(now, retentionDays); r.From.Before(cutoff) {
		r.From, r.Clamped = cutoff, true
		if !r.From.Before(r.To) {
			return r, validation.New("from", "the range is entirely older than the history your plan keeps")
		}
	}
	return r, nil
}

// Cutoff is the oldest instant of history a plan retains.
func Cutoff(now time.Time, retentionDays int) time.Time {
	return now.UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
}

// bucketSizes are the allowed aggregation widths in seconds.
var bucketSizes = []int{60, 300, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400, 172800, 259200}

// MaxPoints is the most points a series may contain.
const MaxPoints = 150

// BucketSeconds picks the smallest bucket that keeps the series within MaxPoints.
func BucketSeconds(window time.Duration) int {
	secs := int(window / time.Second)
	for _, b := range bucketSizes {
		if (secs+b-1)/b <= MaxPoints {
			return b
		}
	}
	return bucketSizes[len(bucketSizes)-1]
}
