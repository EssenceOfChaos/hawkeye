package checks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDiscarded means the monitor was deleted or paused while its check ran, so the result was dropped.
var ErrDiscarded = errors.New("monitor no longer active; result discarded")

// Outcome describes what recording a result changed.
type Outcome struct {
	Before, After State
	Transition    Transition
}

// Incidents reacts to confirmed state changes inside the transaction that records the result, so a
// status change, its incident and its queued notifications commit together or not at all.
type Incidents interface {
	// Opened is called when an outage is confirmed; failures is the current failure streak.
	Opened(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, failures int, category, summary string) error
	// Resolved is called when a monitor becomes healthy again; successes is the current success streak.
	Resolved(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, successes int) error
	// CertificateExpiring is called once per certificate and warning bucket (30, 14, 7, 1 days, or 0 when expired) as the monitor's certificate
	// approaches its expiry. It queues the warning emails.
	CertificateExpiring(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, expiresAt time.Time, bucket int) error
}

type Store struct {
	pool       *pgxpool.Pool
	thresholds Thresholds
	incidents  Incidents // optional
}

func NewStore(pool *pgxpool.Pool, th Thresholds, inc Incidents) *Store {
	return &Store{pool: pool, thresholds: th.normalized(), incidents: inc}
}

// Record persists a result and updates the monitor's counters and status in one transaction. The monitor
// row is locked, so concurrent results for one monitor are applied one at a time.
func (s *Store) Record(ctx context.Context, monitorID uuid.UUID, r Result) (Outcome, error) {
	var out Outcome
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var enabled, maintenance bool
		err := tx.QueryRow(ctx, `SELECT current_status, consecutive_failures, consecutive_successes, enabled, in_maintenance(organization_id, id, $2)
			FROM monitors WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, monitorID, r.FinishedAt).
			Scan(&out.Before.Status, &out.Before.Failures, &out.Before.Successes, &enabled, &maintenance)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled {
			return ErrDiscarded
		}
		if err != nil {
			return err
		}

		summary := truncate(r.ErrorSummary, maxSummaryLen)
		if _, err := tx.Exec(ctx, `INSERT INTO check_results
			(monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms, error_category, error_summary, in_maintenance)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9)`,
			monitorID, r.CheckedAt, r.FinishedAt, r.Success, r.StatusCode, r.ResponseTimeMs, r.ErrorCategory, summary, maintenance); err != nil {
			return fmt.Errorf("insert result: %w", err)
		}

		// During maintenance the result is recorded but changes nothing about the monitor's health: no streaks, no status
		// change, no incident, no alert. The state picks up where it left off when the window ends.
		if maintenance {
			out.After = out.Before
		} else {
			out.After, out.Transition = Apply(out.Before, r.Success, s.thresholds)
		}
		_, err = tx.Exec(ctx, `UPDATE monitors SET current_status = $2, consecutive_failures = $3, consecutive_successes = $4,
			last_checked_at = $5, last_success_at = CASE WHEN $6 THEN $5 ELSE last_success_at END
			WHERE id = $1`, monitorID, out.After.Status, out.After.Failures, out.After.Successes, r.FinishedAt, r.Success)
		if err != nil {
			return err
		}
		if err := s.recordCertificate(ctx, tx, monitorID, r); err != nil {
			return err
		}
		if s.incidents == nil || maintenance {
			return nil
		}
		if out.Transition == WentDown {
			return s.incidents.Opened(ctx, tx, monitorID, out.After.Failures, r.ErrorCategory, summary)
		}
		// Any move into "up" closes an open incident. This covers confirmed recovery and also the case where
		// editing a monitor reset its state to "unknown" while an incident was open.
		if out.After.Status == StatusUp && out.Before.Status != StatusUp {
			return s.incidents.Resolved(ctx, tx, monitorID, out.After.Successes)
		}
		return nil
	})
	return out, err
}

// recordCertificate stores the certificate expiry the check saw and queues a warning when the certificate has entered a new bucket.
// Renewing the certificate (a new expiry date) starts the schedule again. Runs inside the result's transaction, so the stored state and the
// queued warnings commit together.
func (s *Store) recordCertificate(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, r Result) error {
	if r.CertExpiresAt == nil {
		return nil
	}
	exp := r.CertExpiresAt.UTC().Truncate(time.Second)
	var notified *int
	err := tx.QueryRow(ctx, `UPDATE monitors SET ssl_expires_at = $2, ssl_checked_at = $3,
			ssl_notified_bucket = CASE WHEN ssl_notified_for IS NOT DISTINCT FROM $2 THEN ssl_notified_bucket END,
			ssl_notified_for = CASE WHEN ssl_notified_for IS NOT DISTINCT FROM $2 THEN ssl_notified_for END
		WHERE id = $1 RETURNING ssl_notified_bucket`, monitorID, exp, r.FinishedAt).Scan(&notified)
	if err != nil {
		return fmt.Errorf("record certificate: %w", err)
	}
	bucket, warn := SSLBucket(exp, r.FinishedAt)
	if !warn || (notified != nil && bucket >= *notified) || s.incidents == nil {
		return nil
	}
	if err := s.incidents.CertificateExpiring(ctx, tx, monitorID, exp, bucket); err != nil {
		return fmt.Errorf("queue certificate warning: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE monitors SET ssl_notified_bucket = $2, ssl_notified_for = $3 WHERE id = $1`, monitorID, bucket, exp)
	return err
}
