package incidentanalysis

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/incidents"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
)

const (
	// MaxAttempts is how many times a run is tried when the model can't be reached before it is marked failed.
	MaxAttempts = 3
	// HourlyLimit is the most analyses one organization runs in an hour, so a burst of incidents can't use up the month in minutes. Runs over it
	// wait (they are not dropped).
	HourlyLimit = 30
	// lease is how long a claimed run is held before another worker may take it over (the model call times out well before).
	lease = 2 * time.Minute
)

// Job runs pending analyses. Several instances can run: each run is claimed with FOR UPDATE SKIP LOCKED and held by a lease.
type Job struct {
	pool     *pgxpool.Pool
	ai       Analyzer
	m        *metrics.Metrics
	log      *slog.Logger
	rep      errtrack.Reporter
	interval time.Duration
}

// NewJob returns a Job that analyzes with ai. m may be nil (tests).
func NewJob(pool *pgxpool.Pool, ai Analyzer, m *metrics.Metrics, log *slog.Logger) *Job {
	return &Job{pool: pool, ai: ai, m: m, log: log, rep: errtrack.Noop{}, interval: 2 * time.Second}
}

// WithReporter sends failures to an error tracker.
func (j *Job) WithReporter(r errtrack.Reporter) *Job {
	j.rep = r
	return j
}

// Run works through pending analyses until ctx is canceled, waiting a moment whenever there is nothing to do.
func (j *Job) Run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := j.RunOne(ctx)
		if err != nil && ctx.Err() == nil {
			j.log.Error("incident analysis failed", "error", err)
			j.rep.Capture(ctx, err, map[string]string{"component": "incident_analysis"})
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(j.interval + time.Duration(rand.Float64()*float64(j.interval)/2)):
		}
	}
}

// run is one claimed analysis.
type run struct {
	incidentID, orgID uuid.UUID
	attempts          int
	requestedAt       time.Time
}

// RunOne claims and runs one analysis. It reports whether there was one.
func (j *Job) RunOne(ctx context.Context) (bool, error) {
	var r run
	err := j.pool.QueryRow(ctx, `UPDATE incident_analyses a SET status = 'running', locked_until = now() + $1::interval, attempts = a.attempts + 1
		WHERE a.incident_id = (
			SELECT incident_id FROM incident_analyses
			WHERE (status = 'pending' AND run_at <= now()) OR (status = 'running' AND locked_until < now())
			ORDER BY run_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1)
		RETURNING a.incident_id, a.organization_id, a.attempts, a.requested_at`, lease).Scan(&r.incidentID, &r.orgID, &r.attempts, &r.requestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	admitted, err := j.admit(ctx, r)
	if err != nil || !admitted {
		return true, err
	}
	facts, ok, err := j.facts(ctx, r.incidentID)
	if err != nil {
		return true, errors.Join(err, j.refund(ctx, r), j.retry(ctx, r, 0))
	}
	if !ok { // the snapshot is gone (the incident is being deleted): nothing to analyze
		return true, errors.Join(j.refund(ctx, r), j.finish(ctx, r, "off"))
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	start := time.Now()
	res, usage, err := j.ai.Analyze(callCtx, facts)
	j.countTokens(usage)
	switch {
	case err == nil:
		j.count("done")
		j.log.InfoContext(ctx, "incident analysis", "incident_id", r.incidentID, "status", "done", "category", res.Category,
			"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "duration_ms", time.Since(start).Milliseconds())
		_, err := j.pool.Exec(ctx, `UPDATE incident_analyses SET status = 'done', locked_until = NULL, finished_at = now(), category = $3,
				likely_cause = $4, evidence = $5, next_step = $6, confidence = $7, model = $8, input_tokens = $9, output_tokens = $10
			WHERE incident_id = $1 AND requested_at = $2`,
			r.incidentID, r.requestedAt, res.Category, res.LikelyCause, res.Evidence, res.NextStep, res.Confidence, Model, usage.InputTokens, usage.OutputTokens)
		return true, err
	case errors.Is(err, ErrRefused):
		j.count("declined")
		j.log.InfoContext(ctx, "incident analysis", "incident_id", r.incidentID, "status", "declined")
		return true, errors.Join(j.refund(ctx, r), j.finish(ctx, r, "declined"))
	case ctx.Err() != nil: // shutting down: give the run back as it was
		bg := context.WithoutCancel(ctx)
		return true, errors.Join(j.refund(bg, r), j.retry(bg, r, -1))
	default:
		j.log.WarnContext(ctx, "incident analysis unavailable", "incident_id", r.incidentID, "attempt", r.attempts, "error", err,
			"duration_ms", time.Since(start).Milliseconds())
		if err := j.refund(ctx, r); err != nil {
			return true, err
		}
		if r.attempts >= MaxAttempts {
			j.count("failed")
			return true, j.finish(ctx, r, "failed")
		}
		j.count("retry")
		return true, j.retry(ctx, r, 0)
	}
}

// admit checks, under a per-organization lock, that the organization still has analysis on, that its plan includes it, and that the run fits in
// the month's allowance and the hourly limit; then it counts the run. A run that doesn't fit is finished ("off" or "over_limit") or put back.
func (j *Job) admit(ctx context.Context, r run) (bool, error) {
	tx, err := j.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('ai_analysis:' || $1::text, 0))`, r.orgID); err != nil {
		return false, err
	}
	var on bool
	if err := tx.QueryRow(ctx, `SELECT ai_analysis_enabled FROM organizations WHERE id = $1`, r.orgID).Scan(&on); err != nil {
		return false, err
	}
	p, err := plans.EffectivePlan(ctx, tx, r.orgID)
	if err != nil {
		return false, err
	}
	if !on || p.MaxAIAnalyses <= 0 {
		j.count("off")
		return false, j.finishTx(ctx, tx, r, "off")
	}
	var month, hour int
	if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE created_at > now() - interval '1 hour')
		FROM ai_analysis_usage
		WHERE organization_id = $1 AND created_at >= `+monthStart+` AND NOT (incident_id = $2 AND requested_at = $3)`,
		r.orgID, r.incidentID, r.requestedAt).Scan(&month, &hour); err != nil {
		return false, err
	}
	if month >= p.MaxAIAnalyses {
		j.count("over_limit")
		return false, j.finishTx(ctx, tx, r, "over_limit")
	}
	if hour >= HourlyLimit {
		// Not an attempt: try again in a few minutes, without counting it.
		if _, err := tx.Exec(ctx, `UPDATE incident_analyses SET status = 'pending', locked_until = NULL, attempts = attempts - 1,
			run_at = now() + interval '5 minutes' WHERE incident_id = $1 AND requested_at = $2`, r.incidentID, r.requestedAt); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ai_analysis_usage (organization_id, incident_id, requested_at) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, r.orgID, r.incidentID, r.requestedAt); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// monthStart is the start of the current calendar month in UTC, in SQL.
const monthStart = `(date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`

// facts gathers what the model is sent. ok is false when the incident has no snapshot (any more).
func (j *Job) facts(ctx context.Context, incidentID uuid.UUID) (Facts, bool, error) {
	var kind string
	var category, summary *string
	var expected *int
	var assertions []checks.Assertion
	err := j.pool.QueryRow(ctx, `SELECT m.kind, i.cause_category, i.summary, m.expected_status_code, COALESCE(m.assertions, '[]')
		FROM incidents i JOIN monitors m ON m.id = i.monitor_id WHERE i.id = $1`, incidentID).Scan(&kind, &category, &summary, &expected, &assertions)
	if errors.Is(err, pgx.ErrNoRows) {
		return Facts{}, false, nil
	}
	if err != nil {
		return Facts{}, false, err
	}
	s, err := incidents.LoadSnapshot(ctx, j.pool, incidentID)
	if err != nil || s == nil {
		return Facts{}, false, err
	}
	return FactsFrom(kind, deref(category), deref(summary), expected, assertions, *s), true, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// refund removes the run's usage: a declined or failed run doesn't count against the allowance.
func (j *Job) refund(ctx context.Context, r run) error {
	_, err := j.pool.Exec(ctx, `DELETE FROM ai_analysis_usage WHERE incident_id = $1 AND requested_at = $2`, r.incidentID, r.requestedAt)
	return err
}

// retry puts the run back to try again after a pause that grows with each attempt. adjust is added to its attempts (-1 when the run didn't
// really get its turn).
func (j *Job) retry(ctx context.Context, r run, adjust int) error {
	_, err := j.pool.Exec(ctx, `UPDATE incident_analyses SET status = 'pending', locked_until = NULL, attempts = GREATEST(attempts + $3, 0),
		run_at = now() + make_interval(secs => 60 * GREATEST(attempts + $3, 0))
		WHERE incident_id = $1 AND requested_at = $2`, r.incidentID, r.requestedAt, adjust)
	return err
}

// finish ends the run with a status other than done.
func (j *Job) finish(ctx context.Context, r run, status string) error {
	_, err := j.pool.Exec(ctx, `UPDATE incident_analyses SET status = $3, locked_until = NULL, finished_at = now()
		WHERE incident_id = $1 AND requested_at = $2`, r.incidentID, r.requestedAt, status)
	return err
}

func (j *Job) finishTx(ctx context.Context, tx pgx.Tx, r run, status string) error {
	if _, err := tx.Exec(ctx, `UPDATE incident_analyses SET status = $3, locked_until = NULL, finished_at = now()
		WHERE incident_id = $1 AND requested_at = $2`, r.incidentID, r.requestedAt, status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (j *Job) count(status string) {
	if j.m != nil {
		j.m.AIAnalyses.WithLabelValues(status).Inc()
	}
}

func (j *Job) countTokens(u Usage) {
	if j.m != nil {
		j.m.AIAnalysisTokens.WithLabelValues("input").Add(float64(u.InputTokens))
		j.m.AIAnalysisTokens.WithLabelValues("output").Add(float64(u.OutputTokens))
	}
}
