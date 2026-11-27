// Package retention deletes data that has aged out of each organization's plan, in small batches so normal
// traffic is never blocked, and tidies expired credentials.
//
// Policy:
//   - check results older than the plan's history window are deleted, and so are their hourly totals (internal/rollups); monitors checked more
//     often than once a minute keep their individual results for 30 days (rollups.RawRetention) and their totals for the whole window;
//   - resolved incidents and slowdowns older than the window are deleted (with their delivery records and failure snapshots);
//   - a failure snapshot's body excerpt is cleared 30 days after its incident resolved, whatever the plan (the rest of the snapshot stays);
//   - open incidents, open slowdowns and live monitors are NEVER deleted, whatever the plan (a downgrade does not delete monitors);
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

	"hawkeye/internal/audit"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/reports"
	"hawkeye/internal/rollups"
)

// excerptRetention is how long a failure snapshot keeps its body excerpt after the incident resolved (incidents.ExcerptRetention; repeated here
// so this package needn't import the incident engine).
const excerptRetention = 30 * 24 * time.Hour

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
	CheckResults, Rollups, Incidents, Slowdowns, Monitors, Sessions, Tokens, BillingEvents, RateLimits, SentEmails, AuditEvents, Deploys, Excerpts int
	Skipped                                                                                                                                        bool // another instance holds the lock
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

// Run executes a pass immediately and then every Interval until ctx is canceled.
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
			j.log.Info("retention pass complete", "check_results", st.CheckResults, "check_rollups", st.Rollups, "incidents", st.Incidents, "slowdowns", st.Slowdowns,
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
		// Monitors checked more often than once a minute keep individual results for rollups.RawRetention, in whole hours, and never past the
		// hours the totals already cover: deleting a result the totals don't include yet would lose it from uptime and status pages.
		if raw := j.now().UTC().Add(-rollups.RawRetention).Truncate(time.Hour); raw.After(cutoff) {
			through, err := rollups.Through(ctx, j.pool)
			note(err)
			if err == nil && !through.IsZero() {
				if through.Before(raw) {
					raw = through
				}
				n, err = j.batches(ctx, `DELETE FROM check_results WHERE id IN (
						SELECT r.id FROM check_results r JOIN monitors m ON m.id = r.monitor_id
						WHERE m.organization_id = $1 AND m.check_interval_seconds < `+fmt.Sprint(rollups.SubMinute)+` AND r.checked_at < $2 LIMIT $3)`, id, raw)
				st.CheckResults += n
				note(err)
			}
		}
		// An hour's totals go once the whole hour is past the cutoff, like the last of its results. Rollups have no id, so rows are picked by ctid.
		n, err = j.batches(ctx, `DELETE FROM check_rollups WHERE ctid IN (
				SELECT c.ctid FROM check_rollups c JOIN monitors m ON m.id = c.monitor_id
				WHERE m.organization_id = $1 AND c.hour <= $2::timestamptz - interval '1 hour' LIMIT $3)`, id, cutoff)
		st.Rollups += n
		note(err)
		n, err = j.batches(ctx, `DELETE FROM incidents WHERE id IN (
				SELECT i.id FROM incidents i JOIN monitors m ON m.id = i.monitor_id
				WHERE m.organization_id = $1 AND i.status = 'resolved' AND i.resolved_at < $2 LIMIT $3)`, id, cutoff)
		st.Incidents += n
		note(err)
		n, err = j.batches(ctx, `DELETE FROM slowdowns WHERE id IN (
				SELECT sd.id FROM slowdowns sd JOIN monitors m ON m.id = sd.monitor_id
				WHERE m.organization_id = $1 AND sd.status = 'resolved' AND sd.resolved_at < $2 LIMIT $3)`, id, cutoff)
		st.Slowdowns += n
		note(err)
		// Closed DNS changes (accepted or changed back) are history like resolved slowdowns; an open one is never deleted.
		n, err = j.batches(ctx, `DELETE FROM dns_changes WHERE id IN (
				SELECT c.id FROM dns_changes c JOIN monitors m ON m.id = c.monitor_id
				WHERE m.organization_id = $1 AND c.status <> 'open' AND c.closed_at < $2 LIMIT $3)`, id, cutoff)
		st.Slowdowns += n
		note(err)
	}

	// Response text kept from failed checks (opt-in) goes well before the rest of the incident, on every plan.
	st.Excerpts, err = j.batchesNoOrg(ctx, `UPDATE incident_snapshots SET body_excerpt = NULL, body_cleared_at = now() WHERE incident_id IN (
			SELECT s.incident_id FROM incident_snapshots s JOIN incidents i ON i.id = s.incident_id
			WHERE s.body_excerpt IS NOT NULL AND i.status = 'resolved' AND i.resolved_at < $1 LIMIT $2)`, j.now().Add(-excerptRetention))
	note(err)

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
	// Waitlist sign-ups nobody confirmed are dropped once their 48-hour link expires (internal/waitlist).
	n, err := j.batchesNoOrg(ctx, `DELETE FROM waitlist_entries WHERE id IN (
		SELECT id FROM waitlist_entries WHERE confirmed_at IS NULL AND confirm_expires_at < $1 LIMIT $2)`, j.now())
	st.Tokens += n
	note(err)

	// Records of sent weekly summaries only need to cover the week being sent; keep a quarter for investigation.
	st.SentEmails, err2 = j.batchesNoOrg(ctx, `DELETE FROM weekly_summaries WHERE (user_id, organization_id, week) IN (
			SELECT user_id, organization_id, week FROM weekly_summaries WHERE week < $1 LIMIT $2)`, j.now().Add(-91*24*time.Hour))
	note(err2)
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
	// Deploys (internal/deploys) are short maintenance windows a pipeline opens and closes; the security log keeps the record, so a finished one
	// is only listed in the app for 30 days.
	st.Deploys, err2 = j.batchesNoOrg(ctx, `DELETE FROM maintenance_windows WHERE id IN (
			SELECT id FROM maintenance_windows WHERE kind = 'deploy'
				AND COALESCE(ended_at, starts_at + make_interval(mins => duration_minutes)) < $1 LIMIT $2)`, j.now().Add(-30*24*time.Hour))
	note(err2)
	// The security log is kept for a year on every plan (internal/audit).
	st.AuditEvents, err2 = j.batchesNoOrg(ctx, `DELETE FROM audit_events WHERE id IN (
			SELECT id FROM audit_events WHERE occurred_at < $1 LIMIT $2)`, j.now().Add(-audit.Retention))
	note(err2)

	j.m.RetentionDeleted.WithLabelValues("check_results").Add(float64(st.CheckResults))
	j.m.RetentionDeleted.WithLabelValues("check_rollups").Add(float64(st.Rollups))
	j.m.RetentionDeleted.WithLabelValues("incidents").Add(float64(st.Incidents))
	j.m.RetentionDeleted.WithLabelValues("slowdowns").Add(float64(st.Slowdowns))
	j.m.RetentionDeleted.WithLabelValues("monitors").Add(float64(st.Monitors))
	j.m.RetentionDeleted.WithLabelValues("sessions").Add(float64(st.Sessions))
	j.m.RetentionDeleted.WithLabelValues("tokens").Add(float64(st.Tokens))
	j.m.RetentionDeleted.WithLabelValues("billing_events").Add(float64(st.BillingEvents))
	j.m.RetentionDeleted.WithLabelValues("rate_limits").Add(float64(st.RateLimits))
	j.m.RetentionDeleted.WithLabelValues("audit_events").Add(float64(st.AuditEvents))
	j.m.RetentionDeleted.WithLabelValues("deploys").Add(float64(st.Deploys))
	j.m.RetentionDeleted.WithLabelValues("snapshot_excerpts").Add(float64(st.Excerpts))
	return st, firstErr
}

// batches runs a parameterized DELETE ($1 organization, $2 cutoff, $3 limit) until fewer than Batch rows go.
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
