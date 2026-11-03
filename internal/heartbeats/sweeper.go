package heartbeats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/plans"
)

const (
	// IntakeBeat is how often each API process records that pings can reach HawkEye.
	IntakeBeat = 15 * time.Second
	// IntakeGap is how long without that record counts as HawkEye having been unable to receive pings.
	IntakeGap = 90 * time.Second
)

// MarkIntake records that this process can receive pings. When the previous record is older than IntakeGap, pings may have been lost in between,
// and the end of that gap is recorded too (see Sweeper).
func MarkIntake(ctx context.Context, db interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}) error {
	_, err := db.Exec(ctx, `UPDATE ping_intake SET
		gap_ended_at = CASE WHEN seen_at < now() - make_interval(secs => $1) THEN now() ELSE gap_ended_at END,
		seen_at = GREATEST(seen_at, now())`, IntakeGap.Seconds())
	return err
}

// RunIntake calls MarkIntake every IntakeBeat until ctx is canceled. The API processes, which receive the pings, run it.
func RunIntake(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	t := time.NewTicker(IntakeBeat)
	defer t.Stop()
	for {
		if err := MarkIntake(ctx, pool); err != nil && ctx.Err() == nil {
			log.Warn("heartbeat intake mark failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweeperConfig tunes the Sweeper.
type SweeperConfig struct {
	Interval time.Duration // how often to look for overdue heartbeats (default 15 s)
	Batch    int           // at most this many per pass (default 200)
}

// Sweeper records a missed ping for every heartbeat monitor whose deadline has passed, which opens an incident and alerts like a failed website check.
//
// It never blames the customer for HawkEye's own downtime. While the API processes are not marking the intake it decides nothing, and a heartbeat
// whose last ping is older than the end of such a gap gets a full period (and its grace time) after the gap before it can be called missed: its ping
// may have been sent while HawkEye could not receive it.
type Sweeper struct {
	pool  *pgxpool.Pool
	store *checks.Store
	cfg   SweeperConfig
	log   *slog.Logger
	rep   errtrack.Reporter
}

func NewSweeper(pool *pgxpool.Pool, store *checks.Store, cfg SweeperConfig, log *slog.Logger) *Sweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Second
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 200
	}
	return &Sweeper{pool: pool, store: store, cfg: cfg, log: log, rep: errtrack.Noop{}}
}

// WithReporter sends unexpected errors to an error tracker.
func (w *Sweeper) WithReporter(r errtrack.Reporter) *Sweeper {
	w.rep = r
	return w
}

// Run sweeps until ctx is canceled. Several sweepers may run at once: each takes the overdue monitors the others have not locked.
func (w *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		if _, err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("heartbeat sweep failed", "error", err)
			w.rep.Capture(ctx, err, map[string]string{"component": "heartbeats"})
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweepStats summarizes one pass.
type SweepStats struct {
	Missed, Postponed int
	IntakeDown        bool // the API has not marked the intake recently, so nothing was decided
}

type overdue struct {
	id, orgID     uuid.UUID
	period, grace int
	lastPing      *time.Time
	deadline      time.Time
}

// Tick handles the overdue heartbeats of one batch in one transaction.
func (w *Sweeper) Tick(ctx context.Context) (SweepStats, error) {
	var st SweepStats
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var seen, gapEnded, now time.Time
		err := tx.QueryRow(ctx, `SELECT seen_at, gap_ended_at, now() FROM ping_intake`).Scan(&seen, &gapEnded, &now)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("ping_intake has no row")
		}
		if err != nil {
			return err
		}
		if now.Sub(seen) > IntakeGap {
			st.IntakeDown = true
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT id, organization_id, check_interval_seconds, heartbeat_grace_seconds, last_ping_at, ping_deadline FROM monitors
			WHERE kind = 'heartbeat' AND enabled AND deleted_at IS NULL AND ping_deadline <= now()
			ORDER BY ping_deadline FOR UPDATE SKIP LOCKED LIMIT $1`, w.cfg.Batch)
		if err != nil {
			return err
		}
		var due []overdue
		for rows.Next() {
			var o overdue
			if err := rows.Scan(&o.id, &o.orgID, &o.period, &o.grace, &o.lastPing, &o.deadline); err != nil {
				rows.Close()
				return err
			}
			due = append(due, o)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		minInterval := map[uuid.UUID]int{}
		for _, o := range due {
			m, ok := minInterval[o.orgID]
			if !ok {
				p, err := plans.EffectivePlan(ctx, tx, o.orgID)
				if err != nil {
					return fmt.Errorf("plan for org %s: %w", o.orgID, err)
				}
				m = p.MinimumCheckIntervalSeconds
				minInterval[o.orgID] = m
			}
			period := time.Duration(max(o.period, m)) * time.Second
			grace := time.Duration(o.grace) * time.Second

			// HawkEye could not receive pings for a while after the last one: the job may well have pinged during the gap.
			if o.lastPing == nil || gapEnded.After(*o.lastPing) {
				if later := gapEnded.Add(period + grace); later.After(now) {
					if _, err := tx.Exec(ctx, `UPDATE monitors SET ping_deadline = $2 WHERE id = $1`, o.id, later); err != nil {
						return err
					}
					st.Postponed++
					continue
				}
			}

			summary := "No ping received"
			if o.lastPing != nil {
				summary = "No ping since " + o.lastPing.UTC().Format("2006-01-02 15:04 UTC")
			}
			// The result is dated at the deadline, so an incident starts when the ping became overdue rather than when this pass noticed it.
			r := checks.Result{CheckedAt: o.deadline, FinishedAt: o.deadline, ErrorCategory: CatMissed, ErrorSummary: summary}
			if _, err := w.store.RecordTx(ctx, tx, o.id, r); err != nil {
				return err
			}
			// Each further period without a ping is another missed run (it counts against uptime but opens no second incident). After a long
			// pause of the sweeper itself, it does not catch up period by period.
			next := o.deadline.Add(period)
			if !next.After(now) {
				next = now.Add(period)
			}
			if _, err := tx.Exec(ctx, `UPDATE monitors SET ping_deadline = $2 WHERE id = $1`, o.id, next); err != nil {
				return err
			}
			st.Missed++
		}
		return nil
	})
	if err != nil {
		return SweepStats{}, err
	}
	if st.Missed > 0 || st.Postponed > 0 {
		w.log.Info("heartbeat sweep", "missed", st.Missed, "postponed", st.Postponed)
	}
	return st, nil
}
