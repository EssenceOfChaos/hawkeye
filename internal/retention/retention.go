// Package retention deletes data that has aged out of each organization's plan, in small batches so normal
// traffic is never blocked, and tidies expired credentials.
//
// Policy:
//   - check results older than the plan's history window are deleted;
//   - resolved incidents older than the window are deleted (with their delivery records);
//   - open incidents and live monitors are NEVER deleted, whatever the plan (a downgrade does not delete monitors);
//   - monitors the user deleted are purged after a grace period, cascading to their remaining data;
//   - expired sessions and spent or expired one-time tokens are removed.
//
// Deletion is irreversible: if an organization upgrades after data aged out, that history does not return.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/reports"
)

// advisoryLockKey makes sure only one process runs a retention pass at a time (arbitrary constant).
const advisoryLockKey int64 = 1213680466 // 0x48574b52, "HWKR" (decimal because PostgreSQL < 16 has no hex literals)

type Config struct {
	Interval     time.Duration
	Batch        int           // rows per DELETE statement
	Pause        time.Duration // sleep between batches so the database can breathe
	DeletedGrace time.Duration // how long soft-deleted monitors are kept before purge
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = time.Hour
	}
	if c.Batch <= 0 {
		c.Batch = 5000
	}
	if c.Pause < 0 {
		c.Pause = 0
	}
	if c.DeletedGrace <= 0 {
		c.DeletedGrace = 30 * 24 * time.Hour
	}
	return c
}

// Stats counts what one pass removed.
type Stats struct {
	CheckResults, Incidents, Monitors, Sessions, Tokens, BillingEvents, RateLimits int
	Skipped                                                                        bool // another instance holds the lock
}

type Job struct {
	pool *pgxpool.Pool
	cfg  Config
	m    *metrics.Metrics
	log  *slog.Logger
	now  func() time.Time
	rep  errtrack.Reporter
}

func New(pool *pgxpool.Pool, cfg Config, m *metrics.Metrics, log *slog.Logger) *Job {
	return &Job{pool: pool, cfg: cfg.withDefaults(), m: m, log: log, now: time.Now, rep: errtrack.Noop{}}
}

// WithReporter sends failed retention steps to an error tracker.
func (j *Job) WithReporter(r errtrack.Reporter) *Job {
	j.rep = r
	return j
}

// Run executes a pass immediately and then every Interval until ctx is cancelled.
func (j *Job) Run(ctx context.Context) {
	t := time.NewTicker(j.cfg.Interval)
	defer t.Stop()
	for {
		start := time.Now()
		st, err := j.RunOnce(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			j.m.RetentionRuns.WithLabelValues("error").Inc()
			j.log.Error("retention pass failed", "error", err)
		case err == nil && st.Skipped:
			j.m.RetentionRuns.WithLabelValues("skipped").Inc()
		case err == nil:
			j.m.RetentionRuns.WithLabelValues("ok").Inc()
			j.m.RetentionLastSuccess.SetToCurrentTime()
			j.m.RetentionDuration.Set(time.Since(start).Seconds())
			j.log.Info("retention pass complete", "check_results", st.CheckResults, "incidents", st.Incidents,
				"monitors", st.Monitors, "sessions", st.Sessions, "tokens", st.Tokens, "seconds", time.Since(start).Seconds())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce performs one full pass. Errors for one organization do not stop the others; the first is returned at the end.
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
			j.m.RetentionErrors.Inc()
			j.log.Error("retention step failed", "error", err)
			j.rep.Capture(ctx, err, map[string]string{"component": "retention"})
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	rows, err := j.pool.Query(ctx, `SELECT DISTINCT organization_id FROM monitors`)
	if err != nil {
		return st, err
	}
	var orgs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return st, err
		}
		orgs = append(orgs, id)
	}
	rows.Close()

	for _, id := range orgs {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		p, err := plans.EffectivePlan(ctx, j.pool, id)
		if err != nil {
			note(fmt.Errorf("plan for organization: %w", err))
			continue
		}
		cutoff := reports.Cutoff(j.now(), p.HistoryRetentionDays)
		n, err := j.batches(ctx, `DELETE FROM check_results WHERE id IN (
				SELECT r.id FROM check_results r JOIN monitors m ON m.id = r.monitor_id
				WHERE m.organization_id = $1 AND r.checked_at < $2 LIMIT $3)`, id, cutoff)
		st.CheckResults += n
		note(err)
		n, err = j.batches(ctx, `DELETE FROM incidents WHERE id IN (
				SELECT i.id FROM incidents i JOIN monitors m ON m.id = i.monitor_id
				WHERE m.organization_id = $1 AND i.status = 'resolved' AND i.resolved_at < $2 LIMIT $3)`, id, cutoff)
		st.Incidents += n
		note(err)
	}

	var err2 error
	st.Monitors, err2 = j.batchesNoOrg(ctx, `DELETE FROM monitors WHERE id IN (
			SELECT id FROM monitors WHERE deleted_at IS NOT NULL AND deleted_at < $1 LIMIT $2)`, j.now().Add(-j.cfg.DeletedGrace))
	note(err2)
	st.Sessions, err2 = j.batchesNoOrg(ctx, `DELETE FROM sessions WHERE id IN (SELECT id FROM sessions WHERE expires_at < $1 LIMIT $2)`, j.now())
	note(err2)
	week := j.now().Add(-7 * 24 * time.Hour)
	for _, table := range []string{"password_reset_tokens", "email_verification_tokens", "notification_setting_tokens"} {
		n, err := j.batchesNoOrg(ctx, fmt.Sprintf(`DELETE FROM %[1]s WHERE id IN (
			SELECT id FROM %[1]s WHERE used_at < $1 OR expires_at < $1 LIMIT $2)`, table), week)
		st.Tokens += n
		note(err)
	}

	// Shared rate-limit counters only matter for the current window; keep a day for investigation.
	st.RateLimits, err2 = j.batchesNoOrg(ctx, `DELETE FROM rate_limits WHERE (key, window_start) IN (
			SELECT key, window_start FROM rate_limits WHERE window_start < $1 LIMIT $2)`, j.now().Add(-24*time.Hour))
	note(err2)
	// Stored webhook payloads include customer details: keep handled ones 90 days (enough to investigate), failed ones 180.
	var berr error
	st.BillingEvents, berr = j.batchesNoOrg(ctx, `DELETE FROM billing_webhook_events WHERE id IN (
			SELECT id FROM billing_webhook_events WHERE status IN ('processed', 'ignored') AND received_at < $1 LIMIT $2)`, j.now().Add(-90*24*time.Hour))
	note(berr)
	n2, berr := j.batchesNoOrg(ctx, `DELETE FROM billing_webhook_events WHERE id IN (
			SELECT id FROM billing_webhook_events WHERE status = 'failed' AND received_at < $1 LIMIT $2)`, j.now().Add(-180*24*time.Hour))
	st.BillingEvents += n2
	note(berr)

	j.m.RetentionDeleted.WithLabelValues("check_results").Add(float64(st.CheckResults))
	j.m.RetentionDeleted.WithLabelValues("incidents").Add(float64(st.Incidents))
	j.m.RetentionDeleted.WithLabelValues("monitors").Add(float64(st.Monitors))
	j.m.RetentionDeleted.WithLabelValues("sessions").Add(float64(st.Sessions))
	j.m.RetentionDeleted.WithLabelValues("tokens").Add(float64(st.Tokens))
	j.m.RetentionDeleted.WithLabelValues("billing_events").Add(float64(st.BillingEvents))
	j.m.RetentionDeleted.WithLabelValues("rate_limits").Add(float64(st.RateLimits))
	return st, firstErr
}

// batches runs a parameterised DELETE ($1 organization, $2 cutoff, $3 limit) until fewer than Batch rows go.
func (j *Job) batches(ctx context.Context, sql string, org uuid.UUID, cutoff time.Time) (int, error) {
	return j.loop(ctx, func() (int64, error) {
		tag, err := j.pool.Exec(ctx, sql, org, cutoff, j.cfg.Batch)
		return tag.RowsAffected(), err
	})
}

// batchesNoOrg runs a DELETE with ($1 cutoff, $2 limit).
func (j *Job) batchesNoOrg(ctx context.Context, sql string, cutoff time.Time) (int, error) {
	return j.loop(ctx, func() (int64, error) {
		tag, err := j.pool.Exec(ctx, sql, cutoff, j.cfg.Batch)
		return tag.RowsAffected(), err
	})
}

func (j *Job) loop(ctx context.Context, step func() (int64, error)) (int, error) {
	total := 0
	for {
		n, err := step()
		total += int(n)
		if err != nil {
			return total, err
		}
		if int(n) < j.cfg.Batch {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(j.cfg.Pause):
		}
	}
}
