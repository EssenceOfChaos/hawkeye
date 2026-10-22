package domains

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/notifications"
	"hawkeye/internal/plans"
)

// advisoryLockKey makes sure only one process runs a lookup pass at a time (arbitrary constant).
const advisoryLockKey int64 = 1213682244 // 0x48574b44, "HWKD"

// maxLookupBackoff caps the delay after repeated failed lookups of one domain.
const maxLookupBackoff = 24 * time.Hour

type JobConfig struct {
	Interval  time.Duration // how often a pass looks for domains that are due
	Recheck   time.Duration // how long a looked-up domain waits before its next lookup
	Pace      time.Duration // pause between lookups, so registries are not hammered
	Batch     int           // most lookups per organization per pass
	SyncEvery time.Duration // how often every organization's domains are reconciled with its monitors (monitor changes also do it as they happen)
}

func (c JobConfig) withDefaults() JobConfig {
	if c.Interval <= 0 {
		c.Interval = 10 * time.Minute
	}
	if c.Recheck <= 0 {
		c.Recheck = 24 * time.Hour
	}
	if c.Pace < 0 {
		c.Pace = 0
	}
	if c.Batch <= 0 {
		c.Batch = 200
	}
	if c.SyncEvery <= 0 {
		c.SyncEvery = 24 * time.Hour
	}
	return c
}

// Stats counts what one pass did.
type Stats struct {
	Known, Unavailable, Failed, Warnings int
	Skipped                              bool // another instance holds the lock
}

// Job looks up the expiry dates of tracked domains and queues warnings.
type Job struct {
	pool *pgxpool.Pool
	look Looker
	cfg  JobConfig
	m    *metrics.Metrics
	log  *slog.Logger
	rep  errtrack.Reporter
	now  func() time.Time

	lastSync time.Time
}

func NewJob(pool *pgxpool.Pool, look Looker, cfg JobConfig, m *metrics.Metrics, log *slog.Logger) *Job {
	return &Job{pool: pool, look: look, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}, now: time.Now}
}

// WithReporter sends unexpected errors to an error tracker.
func (j *Job) WithReporter(r errtrack.Reporter) *Job {
	j.rep = r
	return j
}

// Run does a pass immediately and then every Interval until ctx is cancelled.
func (j *Job) Run(ctx context.Context) {
	t := time.NewTicker(j.cfg.Interval)
	defer t.Stop()
	for {
		st, err := j.RunOnce(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			j.log.Error("domain lookup pass failed", "error", err)
		case err == nil && !st.Skipped && st.Known+st.Unavailable+st.Failed > 0:
			j.log.Info("domain lookup pass complete", "known", st.Known, "unavailable", st.Unavailable, "failed", st.Failed, "warnings", st.Warnings)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// outcome is what a lookup found out about one name.
type outcome struct {
	res         Result
	unavailable string // reason, when the registry has no expiry date to give
	err         error  // transient failure
}

type due struct {
	id   uuid.UUID
	org  uuid.UUID
	name string
	fail int
}

// RunOnce performs one pass. A failure for one organization or domain does not stop the others; the first error is returned at the end.
func (j *Job) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats
	conn, err := j.pool.Acquire(ctx)
	if err != nil {
		return st, err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, advisoryLockKey).Scan(&locked); err != nil {
		return st, err
	}
	if !locked {
		st.Skipped = true
		return st, nil
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, advisoryLockKey) }()

	var firstErr error
	note := func(err error) {
		if err != nil && ctx.Err() == nil {
			j.log.Error("domain lookup step failed", "error", err)
			j.rep.Capture(ctx, err, map[string]string{"component": "domains"})
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	if j.now().Sub(j.lastSync) >= j.cfg.SyncEvery {
		if err := j.syncAll(ctx); err != nil {
			note(err)
		} else {
			j.lastSync = j.now()
		}
	}

	now := j.now() // after the sync, so domains it just found are due in this pass
	orgs, err := j.orgsWithDueDomains(ctx, now)
	if err != nil {
		return st, err
	}
	cache := map[string]outcome{} // one lookup per name per pass, however many organizations track it
	for _, org := range orgs {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		p, err := plans.EffectivePlan(ctx, j.pool, org)
		if err != nil {
			note(fmt.Errorf("plan for organization: %w", err))
			continue
		}
		list, err := j.dueDomains(ctx, org, p.MaxDomains, now)
		if err != nil {
			note(err)
			continue
		}
		for _, d := range list {
			if ctx.Err() != nil {
				return st, ctx.Err()
			}
			o, seen := cache[d.name]
			if !seen {
				o = j.lookup(ctx, d.name)
				cache[d.name] = o
				if ctx.Err() == nil && j.cfg.Pace > 0 {
					select {
					case <-ctx.Done():
					case <-time.After(j.cfg.Pace):
					}
				}
			}
			if ctx.Err() != nil {
				return st, ctx.Err()
			}
			warned, err := j.record(ctx, d, o, j.now())
			if err != nil {
				note(err)
				continue
			}
			st.Warnings += warned
			switch {
			case o.err != nil:
				st.Failed++
			case o.unavailable != "":
				st.Unavailable++
			default:
				st.Known++
			}
		}
	}
	return st, firstErr
}

func (j *Job) lookup(ctx context.Context, name string) outcome {
	res, err := j.look.Lookup(ctx, name)
	var un *UnavailableError
	switch {
	case err == nil:
		j.count("known")
		return outcome{res: res}
	case errors.As(err, &un):
		j.count("unavailable")
		return outcome{unavailable: un.Reason}
	}
	j.count("error")
	j.log.Warn("domain lookup failed", "domain", name, "error", err)
	return outcome{err: err}
}

func (j *Job) count(result string) {
	if j.m != nil {
		j.m.DomainLookups.WithLabelValues(result).Inc()
	}
}

// syncAll reconciles every organization's domains with its monitors, so monitors that existed before this feature appear and any drift heals.
func (j *Job) syncAll(ctx context.Context) error {
	rows, err := j.pool.Query(ctx, `SELECT organization_id FROM monitors WHERE deleted_at IS NULL UNION SELECT organization_id FROM domains`)
	if err != nil {
		return err
	}
	var orgs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		orgs = append(orgs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range orgs {
		if err := Sync(ctx, j.pool, id); err != nil {
			return fmt.Errorf("sync domains: %w", err)
		}
	}
	return nil
}

func (j *Job) orgsWithDueDomains(ctx context.Context, now time.Time) ([]uuid.UUID, error) {
	rows, err := j.pool.Query(ctx, `SELECT DISTINCT organization_id FROM domains WHERE next_check_at <= $1`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// dueDomains returns the organization's domains that are within the plan's limit (the oldest ones come first) and due for a lookup.
func (j *Job) dueDomains(ctx context.Context, org uuid.UUID, limit int, now time.Time) ([]due, error) {
	rows, err := j.pool.Query(ctx, `SELECT id, organization_id, name, failure_count FROM (
			SELECT id, organization_id, name, failure_count, next_check_at, row_number() OVER (ORDER BY created_at, id) AS rank
			FROM domains WHERE organization_id = $1) d
		WHERE rank <= $2 AND next_check_at <= $3 ORDER BY next_check_at, name LIMIT $4`, org, limit, now, j.cfg.Batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.org, &d.name, &d.fail); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// jitter spreads the next lookups of domains that were looked up together.
func jitter(d time.Duration) time.Duration { return d + time.Duration(rand.Float64()*0.1*float64(d)) }

// lookupBackoff is the wait after the n-th consecutive failed lookup: 1h, 2h, 4h, ... up to a day.
func lookupBackoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 6 {
		return maxLookupBackoff
	}
	return min(time.Hour<<(n-1), maxLookupBackoff)
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}

// record stores a lookup outcome and, for a known expiry date, queues the warning when the registration has entered a new bucket. A renewal (a new
// expiry date) starts the schedule again. Everything happens in one transaction, so the stored state and the queued warnings commit together.
// It returns the number of warnings queued.
func (j *Job) record(ctx context.Context, d due, o outcome, now time.Time) (int, error) {
	warned := 0
	err := pgx.BeginFunc(ctx, j.pool, func(tx pgx.Tx) error {
		switch {
		case o.err != nil:
			_, err := tx.Exec(ctx, `UPDATE domains SET failure_count = failure_count + 1, last_error = $2, next_check_at = $3 WHERE id = $1`,
				d.id, truncate(o.err.Error()), now.Add(lookupBackoff(d.fail+1)))
			return err
		case o.unavailable != "":
			_, err := tx.Exec(ctx, `UPDATE domains SET status = 'unavailable', expires_at = NULL, source = NULL, unavailable_reason = $2, checked_at = $3,
					next_check_at = $4, failure_count = 0, last_error = NULL WHERE id = $1`,
				d.id, truncate(o.unavailable), now, now.Add(jitter(j.cfg.Recheck)))
			return err
		}
		exp := o.res.ExpiresAt.UTC().Truncate(time.Second)
		var notified *int
		err := tx.QueryRow(ctx, `UPDATE domains SET status = 'known', expires_at = $2, source = $3, unavailable_reason = NULL, checked_at = $4,
				next_check_at = $5, failure_count = 0, last_error = NULL,
				notified_bucket = CASE WHEN notified_for IS NOT DISTINCT FROM $2 THEN notified_bucket END,
				notified_for = CASE WHEN notified_for IS NOT DISTINCT FROM $2 THEN notified_for END
			WHERE id = $1 RETURNING notified_bucket`,
			d.id, exp, o.res.Source, now, now.Add(jitter(j.cfg.Recheck))).Scan(&notified)
		if err != nil {
			return err
		}
		bucket, warn := Bucket(exp, now)
		if !warn || (notified != nil && bucket >= *notified) {
			return nil
		}
		n, err := notifications.EnqueueDomain(ctx, tx, d.org, d.id, exp, bucket)
		if err != nil {
			return fmt.Errorf("queue domain warning: %w", err)
		}
		warned = n
		_, err = tx.Exec(ctx, `UPDATE domains SET notified_bucket = $2, notified_for = $3 WHERE id = $1`, d.id, bucket, exp)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("record domain lookup: %w", err)
	}
	return warned, nil
}
