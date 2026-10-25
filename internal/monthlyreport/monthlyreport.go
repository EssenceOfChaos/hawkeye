// Package monthlyreport emails each organization that asked for it the previous month's uptime reports: one PDF per website, to the organization's
// owners, who forward each PDF to the client it belongs to. Nothing is sent to anyone who has not signed up, and the emails come from HawkEye's normal
// sender, so the agency's clients never see HawkEye's address.
//
// Like the onboarding emails, the job holds no schedule. Every pass it looks for organizations whose previous month is not finished yet, and a record
// per owner and email makes sending safe to repeat: a crash, a temporary mail failure or several workers never send anyone the same email twice.
package monthlyreport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/branding"
	"hawkeye/internal/email"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/reportpdf"
	"hawkeye/internal/reports"
)

type Config struct {
	Interval   time.Duration
	AppBaseURL string
	PerEmail   int           // most PDFs attached to one email; a larger account gets several emails (default 20)
	SendAfter  time.Duration // how long after the month ends to wait, so its last checks are in (default 1h)
	Pace       time.Duration // pause between sends, so alerts keep their share of the provider's rate limit
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = time.Hour
	}
	if c.PerEmail <= 0 {
		c.PerEmail = 20
	}
	if c.SendAfter <= 0 {
		c.SendAfter = time.Hour
	}
	if c.Pace < 0 {
		c.Pace = 0
	}
	return c
}

// Stats counts what one pass did.
type Stats struct{ Organizations, Emails int }

type Job struct {
	pool     *pgxpool.Pool
	mail     email.Sender
	branding *branding.Service
	cfg      Config
	m        *metrics.Metrics
	log      *slog.Logger
	rep      errtrack.Reporter
	now      func() time.Time
}

func NewJob(pool *pgxpool.Pool, mail email.Sender, br *branding.Service, cfg Config, m *metrics.Metrics, log *slog.Logger) *Job {
	return &Job{pool: pool, mail: mail, branding: br, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}, now: time.Now}
}

// WithReporter sends failures to an error tracker.
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
			j.log.Error("monthly report pass failed", "error", err, "emails", st.Emails)
			j.rep.Capture(ctx, err, map[string]string{"component": "monthlyreport"})
		case st.Emails > 0:
			j.log.Info("monthly report pass complete", "organizations", st.Organizations, "emails", st.Emails)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type owner struct {
	id    uuid.UUID
	email string
}

// RunOnce handles every organization whose previous month is still to be sent. An error for one organization does not stop the others; a temporary
// mail failure ends the pass (the provider is not answering) and is retried on the next one.
func (j *Job) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats
	now := j.now().UTC()
	thisMonth := reports.MonthStart(now)
	if now.Sub(thisMonth) < j.cfg.SendAfter {
		return st, nil
	}
	month := thisMonth.AddDate(0, -1, 0)
	rows, err := j.pool.Query(ctx, `SELECT b.organization_id, o.name FROM organization_branding b JOIN organizations o ON o.id = b.organization_id
		WHERE b.monthly_report_email
		  AND NOT EXISTS (SELECT 1 FROM monthly_report_runs r WHERE r.organization_id = b.organization_id AND r.month = $1::date)
		ORDER BY b.organization_id`, month)
	if err != nil {
		return st, err
	}
	type org struct {
		id   uuid.UUID
		name string
	}
	var orgs []org
	for rows.Next() {
		var o org
		if err := rows.Scan(&o.id, &o.name); err != nil {
			rows.Close()
			return st, err
		}
		orgs = append(orgs, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	var first error
	for _, o := range orgs {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		n, err := j.organization(ctx, o.id, o.name, month, now)
		st.Emails += n
		st.Organizations++
		if err != nil {
			if first == nil {
				first = fmt.Errorf("organization %s: %w", o.id, err)
			}
			if errors.Is(err, errTemporary) {
				return st, first
			}
			j.log.ErrorContext(ctx, "monthly report failed", "organization_id", o.id, "error", err)
			j.rep.Capture(ctx, err, map[string]string{"component": "monthlyreport"})
		}
	}
	return st, first
}

// errTemporary marks a send failure worth retrying on the next pass.
var errTemporary = errors.New("temporary mail failure")

func (j *Job) organization(ctx context.Context, orgID uuid.UUID, orgName string, month, now time.Time) (int, error) {
	p, err := plans.EffectivePlan(ctx, j.pool, orgID)
	if err != nil {
		return 0, err
	}
	if !p.ReportsEnabled {
		return 0, nil // downgraded since turning it on: not marked done, so an upgrade later this month still gets the report
	}
	owners, err := j.owners(ctx, orgID)
	if err != nil {
		return 0, err
	}
	var data []reports.Monthly
	ids, err := j.monitorIDs(ctx, orgID)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		m, err := reports.BuildMonthly(ctx, j.pool, orgID, id, month, now, p.HistoryRetentionDays)
		if errors.Is(err, reports.ErrMonitorNotFound) {
			continue // deleted in the meantime
		}
		if err != nil {
			return 0, err
		}
		if m.HasData() {
			data = append(data, m)
		}
	}
	if len(data) == 0 || len(owners) == 0 {
		return 0, j.markDone(ctx, orgID, month)
	}
	brand, err := j.branding.Brand(ctx, orgID, p.BrandedReportsEnabled)
	if err != nil {
		return 0, err
	}

	sent := 0
	parts := (len(data) + j.cfg.PerEmail - 1) / j.cfg.PerEmail
	for i := 0; i < parts; i++ {
		chunk := data[i*j.cfg.PerEmail : min((i+1)*j.cfg.PerEmail, len(data))]
		msg, err := j.compose(orgName, brand, month, chunk, i+1, parts)
		if err != nil {
			return sent, err
		}
		for _, o := range owners {
			n, err := j.deliver(ctx, orgID, month, i+1, o, msg)
			sent += n
			if err != nil {
				return sent, err
			}
		}
	}
	return sent, j.markDone(ctx, orgID, month)
}

func (j *Job) owners(ctx context.Context, orgID uuid.UUID) ([]owner, error) {
	rows, err := j.pool.Query(ctx, `SELECT u.id, u.email FROM organization_members om JOIN users u ON u.id = om.user_id
		WHERE om.organization_id = $1 AND om.role = 'owner' AND u.email_verified_at IS NOT NULL AND u.disabled_at IS NULL ORDER BY u.created_at, u.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []owner
	for rows.Next() {
		var o owner
		if err := rows.Scan(&o.id, &o.email); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (j *Job) monitorIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := j.pool.Query(ctx, `SELECT id FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL ORDER BY created_at, id`, orgID)
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

func (j *Job) markDone(ctx context.Context, orgID uuid.UUID, month time.Time) error {
	_, err := j.pool.Exec(ctx, `INSERT INTO monthly_report_runs (organization_id, month) VALUES ($1, $2::date) ON CONFLICT DO NOTHING`, orgID, month)
	return err
}

// compose builds one email: the PDFs for a group of websites, and a short summary so the owner sees the headline numbers without opening each file.
func (j *Job) compose(orgName string, brand reportpdf.Brand, month time.Time, chunk []reports.Monthly, part, parts int) (email.Message, error) {
	label := month.Format("January 2006")
	var facts []email.Fact
	var atts []email.Attachment
	used := map[string]int{}
	for _, m := range chunk {
		pdf, err := reportpdf.Render(m, brand)
		if err != nil {
			return email.Message{}, fmt.Errorf("render report for %s: %w", m.MonitorID, err)
		}
		name := reportpdf.Filename(m.Name, month)
		if used[name]++; used[name] > 1 {
			name = strings.TrimSuffix(name, ".pdf") + fmt.Sprintf("-%d.pdf", used[name])
		}
		atts = append(atts, email.Attachment{Filename: name, ContentType: "application/pdf", Data: pdf})
		value := "Not enough checks"
		if m.Uptime.UptimePercent != nil {
			value = fmt.Sprintf("%s uptime", trimPercent(*m.Uptime.UptimePercent))
		}
		if m.IncidentCount > 0 {
			value += fmt.Sprintf(" · %d incident%s", m.IncidentCount, map[bool]string{true: "", false: "s"}[m.IncidentCount == 1])
		}
		facts = append(facts, email.Fact{Label: m.Name, Value: value})
	}
	intro := fmt.Sprintf("Attached are the %s uptime reports for %s: one PDF per website. Forward each one to the client it belongs to.", label, orgName)
	if brand.Branded() {
		intro += " They carry your name and logo."
	}
	subject := fmt.Sprintf("Your %s uptime reports", label)
	if parts > 1 {
		subject += fmt.Sprintf(" (part %d of %d)", part, parts)
		intro += fmt.Sprintf(" This is email %d of %d, because there are many websites.", part, parts)
	}
	c := email.Content{
		Preheader: fmt.Sprintf("%d website report%s attached, ready to forward.", len(chunk), map[bool]string{true: "", false: "s"}[len(chunk) == 1]),
		Heading:   fmt.Sprintf("Your %s uptime reports", label), Paragraphs: []string{intro}, Facts: facts,
		ButtonLabel: "Open report settings", ButtonURL: j.cfg.AppBaseURL + "/reports",
		Note:       fmt.Sprintf("You receive this because monthly report emails are turned on for %s. You can turn them off on the Reports page.", orgName),
		AppBaseURL: j.cfg.AppBaseURL,
	}
	msg := c.Message("", "", subject)
	msg.Attachments = atts
	return msg, nil
}

func trimPercent(p float64) string {
	s := fmt.Sprintf("%.3f", p)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s + "%"
}

// deliver sends the message to one owner unless they already have it. The record is written before sending (at most once, never twice); a temporary
// failure removes it again so the next pass retries.
func (j *Job) deliver(ctx context.Context, orgID uuid.UUID, month time.Time, part int, o owner, msg email.Message) (int, error) {
	tag, err := j.pool.Exec(ctx, `INSERT INTO monthly_report_emails (organization_id, month, part, user_id) VALUES ($1, $2::date, $3, $4) ON CONFLICT DO NOTHING`,
		orgID, month, part, o.id)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, nil // sent earlier, or another worker is sending it
	}
	msg.To = o.email
	err = j.mail.Send(ctx, msg)
	switch {
	case err == nil:
		j.count("sent")
		if j.cfg.Pace > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(j.cfg.Pace):
			}
		}
		return 1, nil
	case errors.Is(err, email.ErrPermanent):
		j.count("refused")
		j.log.WarnContext(ctx, "monthly report email refused", "organization_id", orgID, "user_id", o.id, "error", err)
		return 0, nil // the record stays: this address is not tried again
	}
	j.count("failed")
	if _, delErr := j.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM monthly_report_emails WHERE organization_id = $1 AND month = $2::date AND part = $3 AND user_id = $4`,
		orgID, month, part, o.id); delErr != nil {
		j.log.ErrorContext(ctx, "release monthly report claim failed", "organization_id", orgID, "error", delErr)
	}
	return 0, fmt.Errorf("%w: %v", errTemporary, err)
}

func (j *Job) count(result string) {
	if j.m != nil {
		j.m.MonthlyReportEmails.WithLabelValues(result).Inc()
	}
}
