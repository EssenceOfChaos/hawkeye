package statuspagedomains

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"hawkeye/internal/audit"
	"hawkeye/internal/email"
	"hawkeye/internal/errtrack"
)

// jobLockKey makes sure only one process runs a pass at a time (arbitrary constant, "HWKS").
const jobLockKey int64 = 0x48574b53

// JobConfig sets how the job runs. Interval is how often it looks for due domains; it is also the fastest a new claim is re-checked (FastInterval),
// so keep it at a minute.
type JobConfig struct {
	Interval   time.Duration // default 1 minute
	Batch      int           // most domains checked per pass, default 200
	AppBaseURL string        // for links in emails
	From       string        // the sender of the emails ("" uses the mailer's default)
}

func (c JobConfig) withDefaults() JobConfig {
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	if c.Batch <= 0 {
		c.Batch = 200
	}
	return c
}

// Job re-checks claimed domains on their schedule (state.go), and tells the organization's owner when one goes live, needs attention, is fixed,
// or stops being served. It runs on the primary worker only.
type Job struct {
	svc    *Service
	mail   email.Sender
	audit  *audit.Recorder
	cfg    JobConfig
	checks *prometheus.CounterVec // result=ok|problem|no_answer|error
	log    *slog.Logger
	rep    errtrack.Reporter
}

func NewJob(svc *Service, mail email.Sender, rec *audit.Recorder, cfg JobConfig, log *slog.Logger) *Job {
	return &Job{svc: svc, mail: mail, audit: rec, cfg: cfg.withDefaults(), log: log, rep: errtrack.Noop{}}
}

// WithReporter sends unexpected errors to an error tracker.
func (j *Job) WithReporter(r errtrack.Reporter) *Job {
	j.rep = r
	return j
}

// WithMetrics counts checks by result.
func (j *Job) WithMetrics(c *prometheus.CounterVec) *Job {
	j.checks = c
	return j
}

// Run does a pass immediately and then every Interval until ctx is canceled.
func (j *Job) Run(ctx context.Context) {
	t := time.NewTicker(j.cfg.Interval)
	defer t.Stop()
	for {
		if st, err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
			j.log.Error("status page domain checks failed", "error", err)
		} else if len(st.Changes) > 0 {
			j.log.Info("status page domain checks complete", "checked", st.Checked, "changed", len(st.Changes))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Stats is what one pass did.
type Stats struct {
	Checked int
	Changes []Change
	Skipped bool // another process holds the lock
}

// RunOnce checks every due domain (up to Batch). One domain's failure does not stop the others; the first error is returned at the end.
func (j *Job) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats
	conn, err := j.svc.pool.Acquire(ctx)
	if err != nil {
		return st, err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, jobLockKey).Scan(&locked); err != nil {
		return st, err
	}
	if !locked {
		st.Skipped = true
		return st, nil
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, jobLockKey) }()

	rows, err := j.svc.pool.Query(ctx, `SELECT `+domainCols+` FROM status_page_domains WHERE next_check_at <= $1 ORDER BY next_check_at LIMIT $2`,
		j.svc.now(), j.cfg.Batch)
	if err != nil {
		return st, err
	}
	var due []*Domain
	for rows.Next() {
		d, err := j.svc.scan(rows)
		if err != nil {
			rows.Close()
			return st, err
		}
		due = append(due, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	var firstErr error
	for _, d := range due {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		_, ch, r, err := j.svc.check(ctx, d, false)
		switch {
		case errors.Is(err, ErrNotFound):
			continue // removed or replaced while we looked
		case err != nil:
			j.count("error")
			if ctx.Err() == nil {
				j.log.Error("status page domain check failed", "domain_id", d.ID, "error", err)
				j.rep.Capture(ctx, err, map[string]string{"component": "statuspagedomains"})
				if firstErr == nil {
					firstErr = err
				}
			}
			continue
		}
		st.Checked++
		switch {
		case r.OK():
			j.count("ok")
		case r.Transient:
			j.count("no_answer")
		default:
			j.count("problem")
		}
		if ch != nil {
			st.Changes = append(st.Changes, *ch)
			j.announce(ctx, *ch)
		}
	}
	return st, firstErr
}

func (j *Job) count(result string) {
	if j.checks != nil {
		j.checks.WithLabelValues(result).Inc()
	}
}

// pageInfo is what the emails and the log name a domain's page by.
type pageInfo struct {
	org   uuid.UUID
	title string
	slug  string
}

func (j *Job) page(ctx context.Context, pageID uuid.UUID) (pageInfo, error) {
	var p pageInfo
	err := j.svc.pool.QueryRow(ctx, `SELECT organization_id, title, slug FROM status_pages WHERE id = $1`, pageID).Scan(&p.org, &p.title, &p.slug)
	return p, err
}

// announce records a state change in the organization's security log and emails its owner. A lost email is not retried: the editor shows the
// state and the problem, so the email is a convenience, and repeating one would be worse than missing it.
func (j *Job) announce(ctx context.Context, ch Change) {
	p, err := j.page(ctx, ch.Domain.PageID)
	if err != nil {
		j.log.Error("status page for domain change", "domain_id", ch.Domain.ID, "error", err)
		return
	}
	action := map[string]string{StateActive: audit.StatusPageDomainVerified, StateFailing: audit.StatusPageDomainFailing,
		StateDisabled: audit.StatusPageDomainDisabled}[ch.Domain.State]
	if action != "" {
		details := map[string]any{"hostname": ch.Domain.Hostname}
		if ch.Domain.State == StateFailing {
			details["problem"] = ch.Domain.LastError
		}
		j.audit.Record(ctx, audit.Event{Action: action, OrganizationID: p.org, Actor: audit.Actor{Type: audit.ActorSystem},
			TargetType: "status_page", TargetID: ch.Domain.PageID.String(), TargetName: p.title, Details: details})
	}
	subject, c, ok := changeEmail(ch, p, j.cfg.AppBaseURL)
	if !ok || j.mail == nil {
		return
	}
	to, err := j.owners(ctx, p.org)
	if err != nil {
		j.log.Error("owners for domain email", "organization_id", p.org, "error", err)
		return
	}
	for _, addr := range to {
		if err := j.mail.Send(ctx, c.Message(j.cfg.From, addr, subject)); err != nil && ctx.Err() == nil {
			j.log.Warn("status page domain email not sent", "domain_id", ch.Domain.ID, "error", err)
			if !errors.Is(err, email.ErrPermanent) {
				j.rep.Capture(ctx, err, map[string]string{"component": "statuspagedomains"})
			}
		}
	}
}

// owners are the people who can change the domain: the organization's owner, with a verified address on an account that is not disabled.
func (j *Job) owners(ctx context.Context, org uuid.UUID) ([]string, error) {
	rows, err := j.svc.pool.Query(ctx, `SELECT u.email FROM organization_members m JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 AND m.role = 'owner' AND u.email_verified_at IS NOT NULL AND u.disabled_at IS NULL`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// changeEmail writes the email for a state change; ok is false for a change nobody needs to hear about.
func changeEmail(ch Change, p pageInfo, appBaseURL string) (subject string, c email.Content, ok bool) {
	d := ch.Domain
	settings := fmt.Sprintf("%s/status-pages/%s", appBaseURL, d.PageID)
	c = email.Content{AppBaseURL: appBaseURL, Facts: []email.Fact{{Label: "Status page", Value: p.title}, {Label: "Domain", Value: d.Hostname}}}
	switch {
	case d.State == StateActive && ch.From == StateFailing:
		subject = d.Hostname + " is working again"
		c.Tone, c.Heading = email.ToneSuccess, "Your status page's domain is working again"
		c.Paragraphs = []string{"HawkEye checked the DNS records for " + d.Hostname + " again and they are right, so nothing more is needed."}
		c.ButtonLabel, c.ButtonURL = "Open the status page", "https://"+d.Hostname
	case d.State == StateActive:
		subject = "Your status page is live on " + d.Hostname
		c.Tone, c.Heading = email.ToneSuccess, "Your status page is live on "+d.Hostname
		c.Paragraphs = []string{
			"HawkEye found both DNS records, so the status page is now served at https://" + d.Hostname + ".",
			"The first visit can take a few seconds while HawkEye gets its certificate. HawkEye renews it by itself.",
		}
		c.ButtonLabel, c.ButtonURL = "Open the status page", "https://"+d.Hostname
		c.Note = "Leave both DNS records in place: HawkEye checks them every day."
	case d.State == StateFailing:
		until := ""
		if d.FailingSince != nil {
			until = " If they aren't right by " + d.FailingSince.Add(GracePeriod).UTC().Format("January 2") + ", HawkEye will stop serving it there."
		}
		subject = "Action needed: the DNS records for " + d.Hostname + " have changed"
		c.Tone, c.Heading = email.ToneAlert, "The DNS records for "+d.Hostname+" have changed"
		c.Paragraphs = []string{d.LastError, "The status page is still served there while you fix the records." + until}
		c.ButtonLabel, c.ButtonURL = "Check the DNS records", settings
	case d.State == StateDisabled:
		subject = "Your status page is no longer served on " + d.Hostname
		c.Tone, c.Heading = email.ToneAlert, "Your status page is no longer served on "+d.Hostname
		c.Paragraphs = []string{
			"The DNS records for " + d.Hostname + " have been wrong for 7 days, so HawkEye stopped serving the status page there.",
			"Its HawkEye address still works: " + appBaseURL + "/status/" + p.slug + ". Once the records are right again, press Check now on the status page's settings.",
		}
		c.ButtonLabel, c.ButtonURL = "Open the status page settings", settings
	default:
		return "", email.Content{}, false
	}
	return subject, c, true
}
