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

	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/regions"
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
	rep  errtrack.Reporter
	regs regions.Set
}

func New(pool *pgxpool.Pool, cfg Config, m *metrics.Metrics, log *slog.Logger) *Scheduler {
	return &Scheduler{pool: pool, cfg: cfg.withDefaults(), m: m, log: log, rnd: rand.Float64, rep: errtrack.Noop{}, regs: regions.Default()}
}

// WithRegions sets the installation's regions (the default is a single "primary" region). A monitor gets one job per region it runs in.
func (s *Scheduler) WithRegions(r regions.Set) *Scheduler {
	s.regs = r
	return s
}

// WithReporter sends unexpected scheduler errors to an error tracker.
func (s *Scheduler) WithReporter(r errtrack.Reporter) *Scheduler {
	s.rep = r
	return s
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
			s.rep.Capture(ctx, err, map[string]string{"component": "scheduler"})
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
	chosen   []string
}

// Tick enqueues jobs for due monitors in one transaction. Monitors are locked with SKIP LOCKED, so
// concurrent schedulers take disjoint sets, and the UNIQUE(monitor_id) constraint on jobs guarantees
// a monitor never has two outstanding jobs.
func (s *Scheduler) Tick(ctx context.Context) (Stats, error) {
	var st Stats
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var now time.Time
		if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
			return err
		}
		// Backpressure is per region, so a region whose workers are down (its queue only grows) cannot stop the others from being checked.
		depth := map[string]int{}
		drows, err := tx.Query(ctx, `SELECT region, count(*) FROM check_jobs GROUP BY region`)
		if err != nil {
			return err
		}
		for drows.Next() {
			var region string
			var n int
			if err := drows.Scan(&region, &n); err != nil {
				drows.Close()
				return err
			}
			depth[region] = n
		}
		drows.Close()
		if err := drows.Err(); err != nil {
			return err
		}
		capacity := s.cfg.MaxQueueDepth * len(s.regs.List())
		for _, r := range s.regs.List() {
			capacity -= depth[r.Code]
		}
		if capacity <= 0 {
			st.Backpressure = true
			return nil
		}

		rows, err := tx.Query(ctx, `SELECT id, organization_id, check_interval_seconds, next_check_at, regions FROM monitors
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
			if err := rows.Scan(&d.id, &d.orgID, &d.interval, &d.dueAt, &d.chosen); err != nil {
				rows.Close()
				return err
			}
			due = append(due, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		type limits struct{ interval, regions int }
		planLimits := map[uuid.UUID]limits{}
		for _, d := range due {
			lim, ok := planLimits[d.orgID]
			if !ok {
				p, err := plans.EffectivePlan(ctx, tx, d.orgID)
				if err != nil {
					return fmt.Errorf("plan for org %s: %w", d.orgID, err)
				}
				lim = limits{p.MinimumCheckIntervalSeconds, p.MaxRegions}
				planLimits[d.orgID] = lim
			}
			min := lim.interval
			// The plan, not the stored value, has the last word on how often a monitor may run.
			eff := time.Duration(max(d.interval, min)) * time.Second
			if now.Sub(d.dueAt) > eff {
				st.Missed++
			}
			// Jitter spreads monitors that were created or restarted together.
			jitter := time.Duration(s.rnd() * float64(min64(time.Duration(float64(eff)*s.cfg.JitterFrac), s.cfg.MaxJitter)))

			for _, region := range s.regs.Effective(d.chosen, lim.regions) {
				if depth[region] >= s.cfg.MaxQueueDepth {
					st.Skipped++ // that region is backed up; try again next interval
					continue
				}
				tag, err := tx.Exec(ctx, `INSERT INTO check_jobs (monitor_id, region) VALUES ($1, $2) ON CONFLICT (monitor_id, region) DO NOTHING`, d.id, region)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					st.Skipped++ // previous check still queued or running
				} else {
					st.Scheduled++
					depth[region]++
				}
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
