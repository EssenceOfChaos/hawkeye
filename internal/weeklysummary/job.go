// Package weeklysummary sends the Monday email: a short summary of each organization's past week (uptime, incidents, the slowest sites, and
// certificates and domains about to expire) to every verified member who has not turned it off.
//
// Like the onboarding emails (internal/lifecycle), nothing is scheduled ahead: a pass every few minutes works out who is due for last week's summary
// and has not had it. A row in weekly_summaries is claimed before each send, so with several workers each person gets each week's email at most once.
// The window closes on Wednesday: a worker that was down on Monday catches up, but nobody gets last week's news at the end of the week. It is on by
// default; a person turns it off in their account or with the email's own unsubscribe link (which stops only this email).
package weeklysummary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/lifecycle"
	"hawkeye/internal/metrics"
)

const (
	// SendFrom is how long after the week ends (Monday 00:00 UTC) sending starts: 07:00 UTC is morning in Europe and before anyone's working day in
	// the Americas, so the email is waiting at the start of the week.
	SendFrom = 7 * time.Hour
	// SendUntil closes the window (Wednesday 24:00 UTC).
	SendUntil = 3 * 24 * time.Hour
	kind      = "weekly_summary" // the metrics label
)

type Config struct {
	Interval   time.Duration
	From       string // sender; required
	ReplyTo    string
	AppBaseURL string
	Address    string        // optional postal address for the footer
	Batch      int           // most emails per pass
	Pace       time.Duration // pause between sends, so alerts keep their share of the provider's rate limit
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = 15 * time.Minute
	}
	if c.Batch <= 0 {
		c.Batch = 200
	}
	if c.Pace < 0 {
		c.Pace = 0
	}
	if c.ReplyTo == "" {
		c.ReplyTo = c.From
	}
	return c
}

type Stats struct{ Sent, Failed int }

type Job struct {
	pool *pgxpool.Pool
	mail email.Sender
	cfg  Config
	m    *metrics.Metrics
	log  *slog.Logger
	rep  errtrack.Reporter
	now  func() time.Time
}

func NewJob(pool *pgxpool.Pool, mail email.Sender, cfg Config, m *metrics.Metrics, log *slog.Logger) *Job {
	return &Job{pool: pool, mail: mail, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}, now: time.Now}
}

// WithReporter sends emails that failed permanently to an error tracker.
func (j *Job) WithReporter(r errtrack.Reporter) *Job {
	j.rep = r
	return j
}

// WithClock replaces the clock (tests).
func (j *Job) WithClock(now func() time.Time) *Job {
	j.now = now
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
			j.log.Error("weekly summary pass failed", "error", err, "sent", st.Sent)
		case st.Sent > 0 || st.Failed > 0:
			j.log.Info("weekly summary pass complete", "sent", st.Sent, "failed", st.Failed)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// LastWeek is the most recent complete week (Monday 00:00 UTC to the next Monday) before now.
func LastWeek(now time.Time) (from, to time.Time) {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	back := (int(day.Weekday()) + 6) % 7 // days since Monday
	to = day.AddDate(0, 0, -back)
	return to.AddDate(0, 0, -7), to
}

type recipient struct {
	userID, orgID uuid.UUID
	orgName       string
	address       string
	token         uuid.UUID
}

// RunOnce sends last week's summaries that are due. Outside the sending window it does nothing. A temporary sending problem ends the pass early
// (everyone left is tried on the next pass); an address the mail server refuses is not retried.
func (j *Job) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats
	now := j.now().UTC()
	from, to := LastWeek(now)
	if since := now.Sub(to); since < SendFrom || since >= SendUntil {
		return st, nil
	}
	due, err := j.due(ctx, from)
	if err != nil {
		return st, fmt.Errorf("find who is due: %w", err)
	}
	summaries := map[uuid.UUID]Summary{} // one per organization per pass, shared by its members
	for _, r := range due {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		s, ok := summaries[r.orgID]
		if !ok {
			if s, err = Build(ctx, j.pool, r.orgID, r.orgName, from, to, now); err != nil {
				return st, fmt.Errorf("summarise organization %s: %w", r.orgID, err)
			}
			summaries[r.orgID] = s
		}
		sent, err := j.send(ctx, r, s, from, now)
		switch {
		case err != nil:
			st.Failed++
			return st, err
		case sent:
			st.Sent++
			if j.cfg.Pace > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(j.cfg.Pace):
				}
			}
		}
	}
	return st, nil
}

// due lists the people owed last week's summary: verified, active, not opted out, members of an organization with at least one monitor, and not sent
// it yet.
func (j *Job) due(ctx context.Context, week time.Time) ([]recipient, error) {
	rows, err := j.pool.Query(ctx, `SELECT u.id, o.id, o.name, u.email, u.unsubscribe_token
		FROM organization_members om
		JOIN users u ON u.id = om.user_id
		JOIN organizations o ON o.id = om.organization_id
		WHERE u.email_verified_at IS NOT NULL AND u.disabled_at IS NULL AND u.weekly_summary_opt_out_at IS NULL
		  AND EXISTS (SELECT 1 FROM monitors m WHERE m.organization_id = o.id AND m.deleted_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM weekly_summaries w WHERE w.user_id = u.id AND w.organization_id = o.id AND w.week = $1)
		ORDER BY o.id, u.id
		LIMIT $2`, week, j.cfg.Batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipient
	for rows.Next() {
		var r recipient
		if err := rows.Scan(&r.userID, &r.orgID, &r.orgName, &r.address, &r.token); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// send claims this week's email for the person and organization, then sends it. A crash between claim and send loses the email rather than repeating
// it: for a summary, silence beats a duplicate. A temporary failure releases the claim so the next pass tries again.
func (j *Job) send(ctx context.Context, r recipient, s Summary, week, now time.Time) (bool, error) {
	tag, err := j.pool.Exec(ctx, `INSERT INTO weekly_summaries (user_id, organization_id, week) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, r.userID, r.orgID, week)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // another worker got there first
	}
	c := s.Content(j.cfg.AppBaseURL, now)
	c.Address = j.cfg.Address
	token := r.token.String()
	c.UnsubscribeURL = j.cfg.AppBaseURL + lifecycle.UnsubscribePath + "?token=" + token + "&list=" + lifecycle.ListWeeklySummary
	c.UnsubscribePostURL = j.cfg.AppBaseURL + lifecycle.UnsubscribeAPIPath + "?token=" + token + "&list=" + lifecycle.ListWeeklySummary
	msg := c.Message(j.cfg.From, r.address, s.Subject())
	msg.ReplyTo = j.cfg.ReplyTo
	err = j.mail.Send(ctx, msg)
	switch {
	case err == nil:
		j.count("sent")
		return true, nil
	case errors.Is(err, email.ErrPermanent):
		j.count("refused")
		j.log.WarnContext(ctx, "weekly summary refused", "user_id", r.userID, "error", err)
		j.rep.Capture(ctx, err, map[string]string{"component": "weeklysummary"})
		return false, nil
	}
	j.count("failed")
	if _, delErr := j.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM weekly_summaries WHERE user_id = $1 AND organization_id = $2 AND week = $3`,
		r.userID, r.orgID, week); delErr != nil {
		j.log.ErrorContext(ctx, "release weekly summary claim failed", "user_id", r.userID, "error", delErr)
	}
	return false, fmt.Errorf("send weekly summary: %w", err)
}

func (j *Job) count(result string) {
	if j.m != nil {
		j.m.LifecycleEmails.WithLabelValues(kind, result).Inc()
	}
}
