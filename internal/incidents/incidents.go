// Package incidents turns confirmed state transitions into incident records and notifications.
package incidents

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/checks"
	"hawkeye/internal/notifications"
	"hawkeye/internal/secrets"
)

// Engine implements checks.Incidents. Its methods run inside the transaction that records the check result.
type Engine struct {
	// Box seals failure snapshots' body excerpts. Without it (heartbeat-only stores, tools), excerpts are not kept; the rest of the snapshot is.
	Box *secrets.Box
}

// Opened records a confirmed outage and what the confirming check saw (snapshot.go). o.Failures is the length of the current failure streak,
// so the incident starts at the first failed check of the streak rather than at the confirming one.
// At most one incident is open per monitor (partial unique index); a second call is a no-op.
func (e Engine) Opened(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, o checks.Outage) error {
	var orgID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT organization_id FROM monitors WHERE id = $1`, monitorID).Scan(&orgID); err != nil {
		return err
	}
	var id uuid.UUID
	var startedAt time.Time
	err := tx.QueryRow(ctx, `INSERT INTO incidents (monitor_id, started_at, cause_category, summary)
		VALUES ($1,
			COALESCE((SELECT min(checked_at) FROM (SELECT checked_at FROM check_results WHERE monitor_id = $1
				ORDER BY checked_at DESC LIMIT $2) s), now()),
			NULLIF($3, ''), NULLIF($4, ''))
		ON CONFLICT (monitor_id) WHERE status = 'open' DO NOTHING
		RETURNING id, started_at`, monitorID, o.Failures, o.Result.ErrorCategory, o.Summary).Scan(&id, &startedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already open: do not notify twice
	}
	if err != nil {
		return err
	}
	if err := saveSnapshot(ctx, tx, e.Box, id, monitorID, o); err != nil {
		return err
	}
	timing, err := notifications.MonitorTiming(ctx, tx, monitorID)
	if err != nil {
		return err
	}
	// A flapping monitor gets one "flapping" alert instead of an alert for every outage (flap.go).
	if flapping, err := flapOutage(ctx, tx, orgID, monitorID, id, timing); flapping || err != nil {
		return err
	}
	// The alert waits for the monitor's alert delay (counted from the first failed check) and for the end of its quiet hours.
	_, err = notifications.Enqueue(ctx, tx, orgID, monitorID, id, notifications.EventOutage, timing.OutageAt(time.Now(), startedAt))
	return err
}

// CertificateExpiring queues the certificate-expiry warning for a monitor's organization (see checks.SSLBuckets).
func (Engine) CertificateExpiring(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, expiresAt time.Time, bucket int) error {
	var orgID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT organization_id FROM monitors WHERE id = $1`, monitorID).Scan(&orgID); err != nil {
		return err
	}
	timing, err := notifications.MonitorTiming(ctx, tx, monitorID)
	if err != nil {
		return err
	}
	_, err = notifications.EnqueueSSL(ctx, tx, orgID, monitorID, expiresAt, bucket, timing.At(time.Now()))
	return err
}

// Resolved closes the monitor's open incident, if any, and enqueues recovery notifications. An outage alert still held (alert delay or quiet hours)
// is canceled instead, and its destinations get no recovery alert. successes is the length of the current success streak; the incident ends at its
// first check.
func (Engine) Resolved(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, successes int) error {
	var id, orgID uuid.UUID
	var paused bool
	err := tx.QueryRow(ctx, `UPDATE incidents i SET status = 'resolved',
			resolved_at = GREATEST(i.started_at, COALESCE(
				(SELECT min(checked_at) FROM (SELECT checked_at FROM check_results WHERE monitor_id = $1
					ORDER BY checked_at DESC LIMIT $2) s), now()))
		FROM monitors m
		WHERE i.monitor_id = $1 AND i.status = 'open' AND m.id = i.monitor_id
		RETURNING i.id, m.organization_id, i.alerts_paused`, monitorID, successes).Scan(&id, &orgID, &paused)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if paused {
		return nil // an outage of a flapping monitor: nobody was told it went down, so nobody is told it's back (the episode's alerts cover it)
	}
	if err := notifications.CancelHeldOutage(ctx, tx, id); err != nil {
		return err
	}
	timing, err := notifications.MonitorTiming(ctx, tx, monitorID)
	if err != nil {
		return err
	}
	_, err = notifications.Enqueue(ctx, tx, orgID, monitorID, id, notifications.EventRecovery, timing.At(time.Now()))
	return err
}

// SlowStarted opens a slowdown for a monitor whose responses have stayed above its threshold, and queues the slow-response alerts. At most one
// slowdown is open per monitor (partial unique index); a second call is a no-op.
func (Engine) SlowStarted(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, thresholdMs, responseMs int, timings *checks.Timings, at time.Time) error {
	// What got slower, compared with the past week: said in the alert and on the monitor's page.
	var cause string
	if timings != nil {
		usual, err := usualTimings(ctx, tx, monitorID, at)
		if err != nil {
			return err
		}
		cause = SlowCause(timings, usual)
	}
	var id, orgID uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO slowdowns (monitor_id, started_at, threshold_ms, peak_ms, cause)
		SELECT id, $2, $3, $4, NULLIF($5, '') FROM monitors WHERE id = $1
		ON CONFLICT (monitor_id) WHERE status = 'open' DO NOTHING
		RETURNING id, (SELECT organization_id FROM monitors WHERE id = $1)`, monitorID, at, thresholdMs, responseMs, cause).Scan(&id, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already open: do not notify twice
	}
	if err != nil {
		return err
	}
	// A slowdown is already confirmed by several slow checks in a row, so the alert delay does not apply; quiet hours do.
	timing, err := notifications.MonitorTiming(ctx, tx, monitorID)
	if err != nil {
		return err
	}
	_, err = notifications.EnqueueSlowdown(ctx, tx, orgID, monitorID, id, notifications.EventSlow, timing.At(time.Now()))
	return err
}

// SlowEnded closes the monitor's open slowdown, if any, and queues the "back to normal" alerts (or cancels a slow alert still held in quiet hours).
func (Engine) SlowEnded(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, at time.Time) error {
	var id, orgID uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE slowdowns sd SET status = 'resolved', resolved_at = GREATEST(sd.started_at, $2)
		FROM monitors m WHERE sd.monitor_id = $1 AND sd.status = 'open' AND m.id = sd.monitor_id
		RETURNING sd.id, m.organization_id`, monitorID, at).Scan(&id, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := notifications.CancelHeldSlow(ctx, tx, id); err != nil {
		return err
	}
	timing, err := notifications.MonitorTiming(ctx, tx, monitorID)
	if err != nil {
		return err
	}
	_, err = notifications.EnqueueSlowdown(ctx, tx, orgID, monitorID, id, notifications.EventSlowRecovery, timing.At(time.Now()))
	return err
}

// Incident is the API representation of an incident.
type Incident struct {
	ID              uuid.UUID  `json:"id"`
	MonitorID       uuid.UUID  `json:"monitor_id"`
	MonitorName     string     `json:"monitor_name"`
	StartedAt       time.Time  `json:"started_at"`
	ResolvedAt      *time.Time `json:"resolved_at"`
	Status          string     `json:"status"`
	CauseCategory   *string    `json:"cause_category"`
	Summary         *string    `json:"summary"`
	DurationSeconds int64      `json:"duration_seconds"` // so far, while the incident is open
	UpdateCount     int        `json:"update_count"`     // notes posted on it (updates.go)
	// Flapping: the monitor was flapping when this outage began. AlertsPaused: so its own outage and recovery alerts were not sent (the
	// "flapping" alert covered it); false again if the monitor then stayed down and the outage was alerted.
	Flapping     bool `json:"flapping"`
	AlertsPaused bool `json:"alerts_paused"`
}
