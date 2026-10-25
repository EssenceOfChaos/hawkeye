// Package lifecycle sends the onboarding emails that nudge new accounts towards their first monitor.
//
// There is no funnel-stage column and nothing is scheduled ahead of time. Each email is a rule: "people whose address was verified between After and
// Until ago, who still have no monitor, and have not been sent this email". A pass runs every few minutes, finds who matches each rule right now, and
// sends. When someone adds a monitor they simply stop matching, so there is nothing to cancel and no way to send "you have not started yet" to
// someone who has.
//
// Safeguards: only verified addresses are mailed (typos never reach a mailbox), nobody gets two of these within MinGap, anyone who unsubscribed or
// whose account is disabled is skipped, accounts verified longer ago than the last rule's Until are never mailed (switching this on does not
// message old accounts), and sends are paced so a batch cannot use up the provider's rate limit that alerts need. Alerts, verification and security
// email never go through here.
package lifecycle

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
	"hawkeye/internal/metrics"
)

// Email kinds. They are stored in lifecycle_emails, so a kind keeps its meaning forever: add new ones, never reuse or rename.
const (
	KindNoMonitor1d = "no_monitor_1d"
	KindNoMonitor3d = "no_monitor_3d"
	KindNoMonitor7d = "no_monitor_7d"
)

// Rule says when one email is due. Age is the time since the person verified their address, so the clock starts when they can first be mailed.
type Rule struct {
	Kind         string
	After, Until time.Duration // eligible while After <= age < Until; Until stops a late or newly enabled job from mailing stale accounts
	Subject      string
}

const day = 24 * time.Hour

// Rules are the no-monitor sequence, in order. The windows do not overlap, so the order here is also the order people receive them.
var Rules = []Rule{
	{Kind: KindNoMonitor1d, After: 1 * day, Until: 3 * day, Subject: "Add your first website to HawkEye (it takes a minute)"},
	{Kind: KindNoMonitor3d, After: 3 * day, Until: 6 * day, Subject: "What HawkEye watches for you"},
	{Kind: KindNoMonitor7d, After: 7 * day, Until: 10 * day, Subject: "Anything we can help with?"},
}

type Config struct {
	Interval   time.Duration
	From       string        // the sender of these emails, e.g. "HawkEye <customer_support@hawkeye.observer>"; required
	ReplyTo    string        // defaults to From; replies must reach a mailbox someone reads
	AppBaseURL string        // links and the logo
	Address    string        // optional postal address for the footer
	Batch      int           // most people mailed per rule per pass
	MinGap     time.Duration // least time between two of these emails to the same person
	Pace       time.Duration // pause between sends, so alerts keep their share of the provider's rate limit
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = 10 * time.Minute
	}
	if c.Batch <= 0 {
		c.Batch = 20
	}
	if c.MinGap <= 0 {
		c.MinGap = 48 * time.Hour
	}
	if c.Pace < 0 {
		c.Pace = 0
	}
	if c.ReplyTo == "" {
		c.ReplyTo = c.From
	}
	return c
}

// Stats counts what one pass did.
type Stats struct{ Sent, Failed int }

type Job struct {
	pool *pgxpool.Pool
	mail email.Sender
	cfg  Config
	m    *metrics.Metrics
	log  *slog.Logger
	rep  errtrack.Reporter
}

func NewJob(pool *pgxpool.Pool, mail email.Sender, cfg Config, m *metrics.Metrics, log *slog.Logger) *Job {
	return &Job{pool: pool, mail: mail, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}}
}

// WithReporter sends emails that failed permanently to an error tracker.
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
			j.log.Error("lifecycle pass failed", "error", err, "sent", st.Sent)
		case st.Sent > 0 || st.Failed > 0:
			j.log.Info("lifecycle pass complete", "sent", st.Sent, "failed", st.Failed)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type candidate struct {
	id    uuid.UUID
	email string
	token uuid.UUID
}

// RunOnce goes through every rule once. A temporary sending problem (the mail server is down) ends the pass early with an error rather than trying
// every remaining person against a server that is not answering; they are tried again on the next pass.
func (j *Job) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats
	for _, r := range Rules {
		people, err := j.due(ctx, r)
		if err != nil {
			return st, fmt.Errorf("find who is due %s: %w", r.Kind, err)
		}
		for _, p := range people {
			if ctx.Err() != nil {
				return st, ctx.Err()
			}
			sent, err := j.send(ctx, r, p)
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
	}
	return st, nil
}

// due lists people who currently match the rule, oldest first. Any monitor in any organization they belong to counts as started, including one they
// deleted afterwards (the row stays for a while): they found their way in, and a "you have not started" email would be wrong.
func (j *Job) due(ctx context.Context, r Rule) ([]candidate, error) {
	rows, err := j.pool.Query(ctx, `SELECT u.id, u.email, u.unsubscribe_token
		FROM users u
		WHERE u.disabled_at IS NULL AND u.lifecycle_opt_out_at IS NULL
		  AND u.email_verified_at IS NOT NULL
		  AND u.email_verified_at <= now() - $1::interval AND u.email_verified_at > now() - $2::interval
		  AND NOT EXISTS (SELECT 1 FROM organization_members om JOIN monitors m ON m.organization_id = om.organization_id WHERE om.user_id = u.id)
		  AND NOT EXISTS (SELECT 1 FROM lifecycle_emails le
		                  WHERE le.user_id = u.id AND (le.kind = $3 OR le.sent_at > now() - $4::interval))
		ORDER BY u.email_verified_at
		LIMIT $5`, r.After, r.Until, r.Kind, j.cfg.MinGap, j.cfg.Batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.email, &c.token); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// send claims the email for this person, then sends it. The claim is the unique (user, kind) row, so with several workers exactly one sends, and a
// crash between claim and send loses the email rather than repeating it: for a nudge, silence beats a duplicate. If the send fails for a temporary
// reason the claim is released so the next pass tries again; if the address is permanently refused the claim stays, so it is not retried forever.
func (j *Job) send(ctx context.Context, r Rule, p candidate) (bool, error) {
	tag, err := j.pool.Exec(ctx, `INSERT INTO lifecycle_emails (user_id, kind) VALUES ($1, $2) ON CONFLICT DO NOTHING`, p.id, r.Kind)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // another worker got there first
	}
	c := Build(r.Kind, j.cfg.AppBaseURL, j.cfg.Address, p.token.String())
	msg := c.Message(j.cfg.From, p.email, r.Subject)
	msg.ReplyTo = j.cfg.ReplyTo
	err = j.mail.Send(ctx, msg)
	switch {
	case err == nil:
		j.count(r.Kind, "sent")
		return true, nil
	case errors.Is(err, email.ErrPermanent):
		j.count(r.Kind, "refused")
		j.log.WarnContext(ctx, "lifecycle email refused", "kind", r.Kind, "user_id", p.id, "error", err)
		j.rep.Capture(ctx, err, map[string]string{"component": "lifecycle", "kind": r.Kind})
		return false, nil
	}
	j.count(r.Kind, "failed")
	// WithoutCancel: releasing the claim must still happen when the pass is being shut down.
	if _, delErr := j.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM lifecycle_emails WHERE user_id = $1 AND kind = $2`, p.id, r.Kind); delErr != nil {
		j.log.ErrorContext(ctx, "release lifecycle claim failed", "kind", r.Kind, "user_id", p.id, "error", delErr)
	}
	return false, fmt.Errorf("send %s: %w", r.Kind, err)
}

func (j *Job) count(kind, result string) {
	if j.m != nil {
		j.m.LifecycleEmails.WithLabelValues(kind, result).Inc()
	}
}
