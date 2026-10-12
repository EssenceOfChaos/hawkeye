package checks

import (
	"context"
	"errors"
	"fmt"

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
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT current_status, consecutive_failures, consecutive_successes, enabled
			FROM monitors WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, monitorID).
			Scan(&out.Before.Status, &out.Before.Failures, &out.Before.Successes, &enabled)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled {
			return ErrDiscarded
		}
		if err != nil {
			return err
		}

		summary := r.ErrorSummary
		if len(summary) > maxSummaryLen {
			summary = summary[:maxSummaryLen]
		}
		if _, err := tx.Exec(ctx, `INSERT INTO check_results
			(monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms, error_category, error_summary)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''))`,
			monitorID, r.CheckedAt, r.FinishedAt, r.Success, r.StatusCode, r.ResponseTimeMs, r.ErrorCategory, summary); err != nil {
			return fmt.Errorf("insert result: %w", err)
		}

		out.After, out.Transition = Apply(out.Before, r.Success, s.thresholds)
		_, err = tx.Exec(ctx, `UPDATE monitors SET current_status = $2, consecutive_failures = $3, consecutive_successes = $4,
			last_checked_at = $5, last_success_at = CASE WHEN $6 THEN $5 ELSE last_success_at END
			WHERE id = $1`, monitorID, out.After.Status, out.After.Failures, out.After.Successes, r.FinishedAt, r.Success)
		if err != nil || s.incidents == nil {
			return err
		}
		if out.Transition == WentDown {
			return s.incidents.Opened(ctx, tx, monitorID, out.After.Failures, r.ErrorCategory, r.ErrorSummary)
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
