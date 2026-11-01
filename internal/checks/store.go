package checks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/regions"
)

// ErrDiscarded means the monitor was deleted or paused while its check ran, so the result was dropped.
var ErrDiscarded = errors.New("monitor no longer active; result discarded")

// Outcome describes what recording a result changed.
type Outcome struct {
	Before, After State
	Transition    Transition
	Speed         SpeedTransition // a confirmed slowdown or return to normal speed (monitors with a slow-response threshold)
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
	// SlowStarted is called when successful checks have stayed above the monitor's response-time threshold for the required number of checks.
	SlowStarted(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, thresholdMs, responseMs int, at time.Time) error
	// SlowEnded is called when they have then stayed at or under it for as long.
	SlowEnded(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, at time.Time) error
}

// speedSettings is a monitor's slow-response configuration and state, read with the monitor row.
type speedSettings struct {
	thresholdMs *int // nil: slow-response alerts are off
	checks      int
	state       SlowState // Slow is true while a slowdown is open
}

type Store struct {
	pool       *pgxpool.Pool
	thresholds Thresholds
	incidents  Incidents // optional
	regions    regions.Set
}

func NewStore(pool *pgxpool.Pool, th Thresholds, inc Incidents) *Store {
	return &Store{pool: pool, thresholds: th.normalized(), incidents: inc, regions: regions.Default()}
}

// WithRegions sets the installation's regions (the default is a single "primary" region).
func (s *Store) WithRegions(r regions.Set) *Store {
	s.regions = r
	return s
}

// Record persists a result and updates the monitor's counters and status in one transaction. The monitor
// row is locked, so concurrent results for one monitor are applied one at a time.
func (s *Store) Record(ctx context.Context, monitorID uuid.UUID, r Result) (Outcome, error) {
	var out Outcome
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var enabled, maintenance bool
		var orgID uuid.UUID
		var chosen []string
		var speed speedSettings
		err := tx.QueryRow(ctx, `SELECT current_status, consecutive_failures, consecutive_successes, enabled, in_maintenance(organization_id, id, $2), organization_id, regions,
				slow_threshold_ms, slow_checks, slow_streak, normal_streak, EXISTS (SELECT 1 FROM slowdowns WHERE monitor_id = monitors.id AND status = 'open')
			FROM monitors WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, monitorID, r.FinishedAt).
			Scan(&out.Before.Status, &out.Before.Failures, &out.Before.Successes, &enabled, &maintenance, &orgID, &chosen,
				&speed.thresholdMs, &speed.checks, &speed.state.SlowStreak, &speed.state.NormalStreak, &speed.state.Slow)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled {
			return ErrDiscarded
		}
		if err != nil {
			return err
		}

		region := r.Region
		if region == "" {
			region = s.regions.Primary()
		}
		summary := truncate(r.ErrorSummary, maxSummaryLen)
		if _, err := tx.Exec(ctx, `INSERT INTO check_results
			(monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms, error_category, error_summary, in_maintenance, region)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, $10)`,
			monitorID, r.CheckedAt, r.FinishedAt, r.Success, r.StatusCode, r.ResponseTimeMs, r.ErrorCategory, summary, maintenance, region); err != nil {
			return fmt.Errorf("insert result: %w", err)
		}

		effective, err := s.effectiveRegions(ctx, tx, orgID, chosen)
		if err != nil {
			return err
		}
		counts := !maintenance && slices.Contains(effective, region)
		switch {
		case !counts:
			// During maintenance the result is recorded but changes nothing about the monitor's health: no streaks, no status
			// change, no incident, no alert. The state picks up where it left off when the window ends. The same goes for a
			// result from a region the monitor no longer uses (a job that was still running when the regions were edited).
			out.After = out.Before
		case len(effective) == 1:
			out.After, out.Transition = Apply(out.Before, r.Success, s.thresholds)
		default:
			var down []string
			out.After, out.Transition, down, err = s.applyRegional(ctx, tx, monitorID, region, effective, r, out.Before)
			if err != nil {
				return err
			}
			if out.Transition == WentDown {
				summary = truncate(fmt.Sprintf("%s (offline from %d of %d locations)", summary, len(down), len(effective)), maxSummaryLen)
			}
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
		if counts && speed.thresholdMs != nil && r.Success && r.ResponseTimeMs != nil {
			if out.Speed, err = s.recordSpeed(ctx, tx, monitorID, region, effective, *r.ResponseTimeMs, speed); err != nil {
				return err
			}
		}
		if s.incidents != nil {
			switch out.Speed {
			case BecameSlow:
				if err := s.incidents.SlowStarted(ctx, tx, monitorID, *speed.thresholdMs, *r.ResponseTimeMs, r.FinishedAt); err != nil {
					return err
				}
			case BackToNormal:
				if err := s.incidents.SlowEnded(ctx, tx, monitorID, r.FinishedAt); err != nil {
					return err
				}
			}
		}
		if s.incidents == nil || out.Transition == NoTransition && out.After.Status == out.Before.Status {
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

// recordSpeed folds one successful check's response time into the monitor's speed state. With one location the streaks live on the monitor;
// with several, each location keeps its own streaks and verdict and the monitor is slow while a quorum (majority) of its locations are, so one
// slow location alone never raises an alert. It also keeps the open slowdown's slowest response up to date.
func (s *Store) recordSpeed(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, region string, effective []string, ms int, sp speedSettings) (SpeedTransition, error) {
	threshold := *sp.thresholdMs
	if sp.state.Slow && ms > threshold {
		if _, err := tx.Exec(ctx, `UPDATE slowdowns SET peak_ms = GREATEST(peak_ms, $2) WHERE monitor_id = $1 AND status = 'open'`, monitorID, ms); err != nil {
			return NoSpeedChange, err
		}
	}
	if len(effective) == 1 {
		st, t := ApplySpeed(sp.state, ms, threshold, sp.checks)
		_, err := tx.Exec(ctx, `UPDATE monitors SET slow_streak = $2, normal_streak = $3 WHERE id = $1`, monitorID, st.SlowStreak, st.NormalStreak)
		return t, err
	}

	var reg SlowState
	if err := tx.QueryRow(ctx, `SELECT slow, slow_streak, normal_streak FROM monitor_region_state WHERE monitor_id = $1 AND region = $2`,
		monitorID, region).Scan(&reg.Slow, &reg.SlowStreak, &reg.NormalStreak); err != nil {
		return NoSpeedChange, err // the row was written by applyRegional just before
	}
	reg, _ = ApplySpeed(reg, ms, threshold, sp.checks)
	if _, err := tx.Exec(ctx, `UPDATE monitor_region_state SET slow = $3, slow_streak = $4, normal_streak = $5 WHERE monitor_id = $1 AND region = $2`,
		monitorID, region, reg.Slow, reg.SlowStreak, reg.NormalStreak); err != nil {
		return NoSpeedChange, err
	}
	var slowRegions int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM monitor_region_state WHERE monitor_id = $1 AND region = ANY($2) AND slow`,
		monitorID, effective).Scan(&slowRegions); err != nil {
		return NoSpeedChange, err
	}
	slowNow := slowRegions >= regions.Quorum(len(effective))
	switch {
	case slowNow && !sp.state.Slow:
		return BecameSlow, nil
	case !slowNow && sp.state.Slow:
		return BackToNormal, nil
	}
	return NoSpeedChange, nil
}

// effectiveRegions is where the monitor runs now. The plan's limit is looked up only for monitors that chose several regions.
func (s *Store) effectiveRegions(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, chosen []string) ([]string, error) {
	if len(chosen) <= 1 {
		return s.regions.Effective(chosen, 1), nil
	}
	p, err := plans.EffectivePlan(ctx, tx, orgID)
	if err != nil {
		return nil, err
	}
	return s.regions.Effective(chosen, p.MaxRegions), nil
}

// applyRegional folds one region's result into that region's own streak, then decides the monitor's overall state: it is down while at
// least a quorum (majority) of its regions are down, so a problem at one location alone never opens an incident. It returns the new
// overall state (with the reporting region's streaks), the transition, and the regions currently down.
func (s *Store) applyRegional(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, region string, effective []string, r Result, before State) (State, Transition, []string, error) {
	var reg State
	err := tx.QueryRow(ctx, `SELECT status, consecutive_failures, consecutive_successes FROM monitor_region_state
		WHERE monitor_id = $1 AND region = $2`, monitorID, region).Scan(&reg.Status, &reg.Failures, &reg.Successes)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return State{}, NoTransition, nil, err
	}
	reg, _ = Apply(reg, r.Success, s.thresholds)
	if _, err := tx.Exec(ctx, `INSERT INTO monitor_region_state (monitor_id, region, status, consecutive_failures, consecutive_successes, last_checked_at, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		ON CONFLICT (monitor_id, region) DO UPDATE SET status = EXCLUDED.status, consecutive_failures = EXCLUDED.consecutive_failures,
			consecutive_successes = EXCLUDED.consecutive_successes, last_checked_at = EXCLUDED.last_checked_at, last_error = EXCLUDED.last_error`,
		monitorID, region, reg.Status, reg.Failures, reg.Successes, r.FinishedAt, truncate(r.ErrorSummary, maxSummaryLen)); err != nil {
		return State{}, NoTransition, nil, err
	}

	rows, err := tx.Query(ctx, `SELECT region, status FROM monitor_region_state WHERE monitor_id = $1 AND region = ANY($2)`, monitorID, effective)
	if err != nil {
		return State{}, NoTransition, nil, err
	}
	defer rows.Close()
	var down []string
	anyUp := false
	for rows.Next() {
		var name, status string
		if err := rows.Scan(&name, &status); err != nil {
			return State{}, NoTransition, nil, err
		}
		switch status {
		case StatusDown:
			down = append(down, name)
		case StatusUp:
			anyUp = true
		}
	}
	if err := rows.Err(); err != nil {
		return State{}, NoTransition, nil, err
	}

	after := State{Status: before.Status, Failures: reg.Failures, Successes: reg.Successes}
	var t Transition
	switch {
	case len(down) >= regions.Quorum(len(effective)):
		after.Status = StatusDown
		if before.Status != StatusDown {
			t = WentDown
		}
	case before.Status == StatusDown:
		after.Status, t = StatusUp, Recovered
	case (before.Status == StatusUnknown || before.Status == "") && anyUp:
		after.Status = StatusUp
	}
	return after, t, down, nil
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
