// Package scheduler decides which monitors are due and enqueues durable check jobs for them.
// It never performs checks itself and is independent of the HTTP API.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
)

type Config struct {
	Interval      time.Duration // how often to look for due monitors
	Batch         int           // max monitors scheduled per tick
	MaxQueueDepth int           // stop scheduling while this many jobs are outstanding
	JitterFrac    float64       // next check is spread over up to this fraction of the interval...
	MaxJitter     time.Duration // ...capped at this duration
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = time.Second
	}
	if c.Batch <= 0 {
		c.Batch = 200
	}
	if c.MaxQueueDepth <= 0 {
		c.MaxQueueDepth = 1000
	}
	if c.JitterFrac <= 0 {
		c.JitterFrac = 0.1
	}
	if c.MaxJitter <= 0 {
		c.MaxJitter = 30 * time.Second
	}
	return c
}

type Scheduler struct {
	pool *pgxpool.Pool
	cfg  Config
	m    *metrics.Metrics
	log  *slog.Logger
	rnd  func() float64 // [0,1); replaceable in tests
}

func New(pool *pgxpool.Pool, cfg Config, m *metrics.Metrics, log *slog.Logger) *Scheduler {
	return &Scheduler{pool: pool, cfg: cfg.withDefaults(), m: m, log: log, rnd: rand.Float64}
}

// Stats summarises one tick.
type Stats struct {
	Scheduled, Skipped, Missed int
	Backpressure               bool
}

// Run ticks until ctx is cancelled. Any number of schedulers may run concurrently.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.m.WorkerErrors.WithLabelValues("schedule").Inc()
			s.log.Error("scheduler tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type dueMonitor struct {
	id       uuid.UUID
	orgID    uuid.UUID
	interval int
	dueAt    time.Time
}

// Tick enqueues jobs for due monitors in one transaction. Monitors are locked with SKIP LOCKED, so
// concurrent schedulers take disjoint sets, and the UNIQUE(monitor_id) constraint on jobs guarantees
// a monitor never has two outstanding jobs.
func (s *Scheduler) Tick(ctx context.Context) (Stats, error) {
	var st Stats
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var depth int
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM check_jobs), now()`).Scan(&depth, &now); err != nil {
			return err
		}
		capacity := s.cfg.MaxQueueDepth - depth
		if capacity <= 0 {
			st.Backpressure = true
			return nil
		}

		rows, err := tx.Query(ctx, `SELECT id, organization_id, check_interval_seconds, next_check_at FROM monitors
			WHERE enabled AND deleted_at IS NULL AND next_check_at <= now()
			ORDER BY next_check_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1`, min(s.cfg.Batch, capacity))
		if err != nil {
			return err
		}
		var due []dueMonitor
		for rows.Next() {
			var d dueMonitor
			if err := rows.Scan(&d.id, &d.orgID, &d.interval, &d.dueAt); err != nil {
				rows.Close()
				return err
			}
			due = append(due, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		planMin := map[uuid.UUID]int{}
		for _, d := range due {
			min, ok := planMin[d.orgID]
			if !ok {
				p, err := plans.EffectivePlan(ctx, tx, d.orgID)
				if err != nil {
					return fmt.Errorf("plan for org %s: %w", d.orgID, err)
				}
				min = p.MinimumCheckIntervalSeconds
				planMin[d.orgID] = min
			}
			// The plan, not the stored value, has the last word on how often a monitor may run.
			eff := time.Duration(max(d.interval, min)) * time.Second
			if now.Sub(d.dueAt) > eff {
				st.Missed++
			}
			// Jitter spreads monitors that were created or restarted together.
			jitter := time.Duration(s.rnd() * float64(min64(time.Duration(float64(eff)*s.cfg.JitterFrac), s.cfg.MaxJitter)))

			tag, err := tx.Exec(ctx, `INSERT INTO check_jobs (monitor_id) VALUES ($1) ON CONFLICT (monitor_id) DO NOTHING`, d.id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				st.Skipped++ // previous check still queued or running
			} else {
				st.Scheduled++
			}
			// Next run is measured from now, not from the old due time: after an outage of the scheduler
			// there is no backlog of catch-up checks.
			if _, err := tx.Exec(ctx, `UPDATE monitors SET next_check_at = $2 WHERE id = $1`, d.id, now.Add(eff+jitter)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Stats{}, err
	}
	s.m.Scheduled.Add(float64(st.Scheduled))
	s.m.Skipped.Add(float64(st.Skipped))
	s.m.Missed.Add(float64(st.Missed))
	if st.Backpressure {
		s.m.Backpressure.Inc()
	}
	return st, nil
}

func min64(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// Lag returns how overdue the most overdue enabled monitor is (0 when none is overdue).
func Lag(ctx context.Context, db plans.DB) (time.Duration, error) {
	var secs float64
	err := db.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(next_check_at)), 0)::float8
		FROM monitors WHERE enabled AND deleted_at IS NULL AND next_check_at <= now()`).Scan(&secs)
	return time.Duration(secs * float64(time.Second)), err
}
