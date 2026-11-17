package rollups

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
)

// JobConfig tunes the rollup job. Zero values take the defaults.
type JobConfig struct {
	Interval time.Duration // how often to look for newly closed hours and late results (default 1 minute)
	// Settle is how long after an hour ends before it is added up, so results still being recorded for it are included (default 15 minutes).
	Settle time.Duration
	// Chunk is the most hours one transaction adds up. Building totals for existing history takes many chunks, with Pause between them, so it
	// never holds the database for long (default 6 hours).
	Chunk time.Duration
	Pause time.Duration // between chunks while catching up (default 200 ms)
	Now   func() time.Time
}

func (c JobConfig) withDefaults() JobConfig {
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	if c.Settle <= 0 {
		c.Settle = 15 * time.Minute
	}
	if c.Chunk < time.Hour {
		c.Chunk = 6 * time.Hour
	}
	if c.Pause <= 0 {
		c.Pause = 200 * time.Millisecond
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Job adds up closed hours of check results into check_rollups and moves rollup_state.through forward. Any number may run: each step locks
// rollup_state, so they take turns.
type Job struct {
	pool *pgxpool.Pool
	cfg  JobConfig
	m    *metrics.Metrics
	log  *slog.Logger
	rep  errtrack.Reporter
}

func NewJob(pool *pgxpool.Pool, cfg JobConfig, m *metrics.Metrics, log *slog.Logger) *Job {
	return &Job{pool: pool, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}}
}

// WithReporter sends unexpected errors to an error tracker.
func (j *Job) WithReporter(r errtrack.Reporter) *Job {
	j.rep = r
	return j
}

// Run steps until caught up, waits Interval, and repeats until ctx is canceled.
func (j *Job) Run(ctx context.Context) {
	for ctx.Err() == nil {
		more, err := j.Step(ctx)
		if err != nil && ctx.Err() == nil {
			j.m.WorkerErrors.WithLabelValues("rollup").Inc()
			j.log.Error("rollup step failed", "error", err)
			j.rep.Capture(ctx, err, map[string]string{"component": "rollups"})
			more = false
		}
		wait := j.cfg.Interval
		if more {
			wait = j.cfg.Pause
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// insertTotals adds up check_results into check_rollups for the rows the WHERE clause (on alias r) selects. The hours must have been deleted first.
const insertTotals = `INSERT INTO check_rollups (monitor_id, hour, in_maintenance, checks, successes, ms_sum, ms_count, ms_max)
	SELECT r.monitor_id, date_trunc('hour', r.checked_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC', r.in_maintenance, count(*), count(*) FILTER (WHERE r.success),
		COALESCE(sum(r.response_time_ms) FILTER (WHERE r.success), 0), count(r.response_time_ms) FILTER (WHERE r.success),
		max(r.response_time_ms) FILTER (WHERE r.success)
	FROM check_results r WHERE `

// Step does one transaction's work and reports whether more is waiting:
//
//  1. Results recorded since the last step for hours already added up (a heartbeat's missed deadline, recorded once the worker is back) have
//     their monitor's hour added up again.
//  2. The next closed hours, up to Chunk, are added up, starting with the hour before through again, which catches results recorded up to an
//     hour after Settle without the lookup in step 1.
//
// Hours are replaced whole (delete, then insert from raw results), so repeating a step changes nothing.
func (j *Job) Step(ctx context.Context) (more bool, err error) {
	target := j.cfg.Now().UTC().Add(-j.cfg.Settle).Truncate(time.Hour)
	var through time.Time
	err = pgx.BeginFunc(ctx, j.pool, func(tx pgx.Tx) error {
		var cur *time.Time
		var seen *int64
		if err := tx.QueryRow(ctx, `SELECT through, seen_id FROM rollup_state FOR UPDATE`).Scan(&cur, &seen); err != nil {
			return err
		}
		// Every result with an id up to maxID is in an hour handled by this step or an earlier one. (An id is taken when a result is inserted, and
		// recording takes milliseconds, so one still uncommitted here, with an id below maxID, and recorded for a past hour, is not a practical case.)
		var maxID int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM check_results`).Scan(&maxID); err != nil {
			return err
		}

		if cur != nil {
			through = cur.UTC()
			if seen != nil && maxID > *seen {
				if err := redoLate(ctx, tx, *seen, maxID, through); err != nil {
					return err
				}
			}
		}

		var from, start time.Time
		if cur == nil {
			// First run: start at the oldest result (or at target, when there are none yet).
			var oldest *time.Time
			if err := tx.QueryRow(ctx, `SELECT min(checked_at) FROM check_results`).Scan(&oldest); err != nil {
				return err
			}
			from = target
			if oldest != nil && oldest.Before(target) {
				from = oldest.UTC().Truncate(time.Hour)
			}
			start = from
		} else {
			from, start = through.Add(-time.Hour), through
		}

		to := start
		if start.Before(target) {
			to = start.Add(j.cfg.Chunk)
			if to.After(target) {
				to = target
			}
			if _, err := tx.Exec(ctx, `DELETE FROM check_rollups WHERE hour >= $1 AND hour < $2`, from, to); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, insertTotals+`r.checked_at >= $1 AND r.checked_at < $2 GROUP BY 1, 2, 3`, from, to); err != nil {
				return err
			}
			more = to.Before(target)
		}
		through = to
		_, err := tx.Exec(ctx, `UPDATE rollup_state SET through = $1, seen_id = $2`, to, maxID)
		return err
	})
	if err != nil {
		return false, err
	}
	j.m.RollupLag.Set(max(0, j.cfg.Now().Sub(through).Seconds()))
	return more, nil
}

// redoLate adds up again each monitor's hour, before done, that gained results with ids in (seen, maxID].
func redoLate(ctx context.Context, tx pgx.Tx, seen, maxID int64, done time.Time) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT monitor_id, date_trunc('hour', checked_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
		FROM check_results WHERE id > $1 AND id <= $2 AND checked_at < $3`, seen, maxID, done)
	if err != nil {
		return err
	}
	var monitors []uuid.UUID
	var hours []time.Time
	for rows.Next() {
		var m uuid.UUID
		var h time.Time
		if err := rows.Scan(&m, &h); err != nil {
			rows.Close()
			return err
		}
		monitors, hours = append(monitors, m), append(hours, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(monitors) == 0 {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM check_rollups c USING unnest($1::uuid[], $2::timestamptz[]) AS late (monitor_id, hour)
		WHERE c.monitor_id = late.monitor_id AND c.hour = late.hour`, monitors, hours); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, insertTotals+`(r.monitor_id, date_trunc('hour', r.checked_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') IN
			(SELECT * FROM unnest($1::uuid[], $2::timestamptz[]))
		AND r.checked_at >= (SELECT min(h) FROM unnest($2::timestamptz[]) h) AND r.checked_at < (SELECT max(h) FROM unnest($2::timestamptz[]) h) + interval '1 hour'
		GROUP BY 1, 2, 3`, monitors, hours)
	return err
}
