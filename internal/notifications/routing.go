package notifications

import (
	"context"
	"fmt"
	"time"
	_ "time/tzdata" // quiet hours name IANA time zones; embed the database so they work on any host

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Who receives an alert.
//
// A destination's monitor_scope is "all" (every monitor; the default) or "tags" (only monitors carrying one of its tags, notification_setting_tags).
// Account-wide alerts that belong to no monitor (domain expiry) go to the "all" destinations. Only enabled, verified destinations ever count.
//
// An alert must never silently reach nobody, so when routing leaves it with no destination it falls back to the team's own email addresses: email
// destinations that belong to a current member of the organization (user_id). It deliberately does not fall back to every destination: for an agency
// those include its clients' chat channels and addresses, and one client must never receive another client's alert. If there is no such address either,
// nobody can be told; the Notifications page warns about uncovered monitors before that happens (Coverage).

// routedRecipients selects the ids of the destinations for an alert. $1 is the organization, $2 the monitor (NULL for account-wide alerts).
const routedRecipients = `WITH eligible AS (
		SELECT s.id, s.channel, s.user_id, s.monitor_scope FROM notification_settings s
		WHERE s.organization_id = $1 AND s.enabled AND s.verified_at IS NOT NULL),
	routed AS (
		SELECT e.id FROM eligible e WHERE e.monitor_scope = 'all' OR EXISTS (
			SELECT 1 FROM notification_setting_tags nt JOIN monitor_tags mt ON mt.tag_id = nt.tag_id
			WHERE nt.setting_id = e.id AND mt.monitor_id = $2::uuid)),
	recipients AS (
		SELECT id FROM routed
		UNION ALL
		SELECT e.id FROM eligible e JOIN organization_members om ON om.organization_id = $1 AND om.user_id = e.user_id
		WHERE e.channel = 'email' AND NOT EXISTS (SELECT 1 FROM routed))`

// Recipients lists the destinations an alert about the monitor (nil: an account-wide alert) would go to now, and whether that is the fallback.
func Recipients(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, orgID uuid.UUID, monitorID *uuid.UUID) (ids []uuid.UUID, fallback bool, err error) {
	rows, err := q.Query(ctx, routedRecipients+` SELECT id, NOT EXISTS (SELECT 1 FROM routed) FROM recipients`, orgID, monitorID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id, &fallback); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	return ids, fallback, rows.Err()
}

// QuietHours is a daily local-time window during which a monitor's alerts are held until it ends. Start and End are minutes after midnight in
// Location; a window with Start > End crosses midnight (22:00 to 07:00).
type QuietHours struct {
	Start, End int
	Location   *time.Location
}

// NewQuietHours builds quiet hours from stored values, or returns nil when they are not set.
func NewQuietHours(start, end *int, tz *string) (*QuietHours, error) {
	if start == nil || end == nil || tz == nil {
		return nil, nil
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return nil, fmt.Errorf("quiet hours time zone %q: %w", *tz, err)
	}
	return &QuietHours{Start: *start, End: *end, Location: loc}, nil
}

// Until returns when the quiet window covering t ends, or t itself when t is outside quiet hours.
func (q *QuietHours) Until(t time.Time) time.Time {
	if q == nil || q.Start == q.End {
		return t
	}
	local := t.In(q.Location)
	minute := local.Hour()*60 + local.Minute()
	at := func(dayOffset, minutes int) time.Time { // a local wall-clock time; time.Date resolves daylight-saving gaps and overlaps
		y, m, d := local.Date()
		return time.Date(y, m, d+dayOffset, minutes/60, minutes%60, 0, 0, q.Location)
	}
	var end time.Time
	switch {
	case q.Start < q.End && minute >= q.Start && minute < q.End: // same-day window
		end = at(0, q.End)
	case q.Start > q.End && minute >= q.Start: // overnight window, before midnight
		end = at(1, q.End)
	case q.Start > q.End && minute < q.End: // overnight window, after midnight
		end = at(0, q.End)
	default:
		return t
	}
	if end.Before(t) { // a daylight-saving jump can put the resolved end before t; never hold into the past
		return t
	}
	return end
}

// Timing is when a monitor's alerts may go out: DelayMinutes after the outage started (outage alerts only), and never inside quiet hours.
type Timing struct {
	DelayMinutes int
	Quiet        *QuietHours
}

// MonitorTiming reads a monitor's alert timing inside the caller's transaction. A stored time zone that no longer loads disables quiet hours rather
// than holding alerts: an alert that goes out at night is better than one that never does.
func MonitorTiming(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID) (Timing, error) {
	var t Timing
	var start, end *int
	var tz *string
	if err := tx.QueryRow(ctx, `SELECT alert_delay_minutes, quiet_start, quiet_end, quiet_timezone FROM monitors WHERE id = $1`, monitorID).
		Scan(&t.DelayMinutes, &start, &end, &tz); err != nil {
		return t, err
	}
	t.Quiet, _ = NewQuietHours(start, end, tz)
	return t, nil
}

// OutageAt is when an outage that started at startedAt may first be announced, given that it was confirmed at now.
func (t Timing) OutageAt(now, startedAt time.Time) time.Time {
	at := startedAt.Add(time.Duration(t.DelayMinutes) * time.Minute)
	if at.Before(now) {
		at = now
	}
	return t.Quiet.Until(at)
}

// At is when any other alert raised now may go out: outside quiet hours.
func (t Timing) At(now time.Time) time.Time { return t.Quiet.Until(now) }

// cancelHeld skips the not-yet-due alerts of a problem that has just ended (outage or slow, matched by the column and id), so nobody is told about a
// problem that is already over. Only deliberately held alerts qualify: scheduled later than they were queued, never attempted, and not yet due. An
// alert that is merely late (a busy or stopped dispatcher) is always sent.
func cancelHeld(ctx context.Context, tx pgx.Tx, column string, id uuid.UUID, event string) error {
	_, err := tx.Exec(ctx, `UPDATE notification_deliveries SET status = 'skipped', suppressed = 'ended_while_held'
		WHERE `+column+` = $1 AND event_type = $2 AND status = 'pending' AND attempt_count = 0
			AND next_attempt_at > created_at AND next_attempt_at > now()`, id, event)
	return err
}
