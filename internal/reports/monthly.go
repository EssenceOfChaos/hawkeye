package reports

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/validation"
)

// MaxListedIncidents is how many incidents a monthly report lists in full. Totals always count every incident.
const MaxListedIncidents = 25

// ErrMonitorNotFound: no live monitor with that id in the organization.
var ErrMonitorNotFound = errors.New("monitor not found")

// DayStat is one calendar day (UTC) of check results.
type DayStat struct {
	Day    time.Time
	Checks int
	Failed int
}

// Available is the share of the day's checks that succeeded, as a fraction, and false when the day had no checks.
func (d DayStat) Available() (float64, bool) {
	if d.Checks == 0 {
		return 0, false
	}
	return float64(d.Checks-d.Failed) / float64(d.Checks), true
}

// IncidentRow is one outage as it overlaps the report's period.
type IncidentRow struct {
	StartedAt  time.Time
	ResolvedAt *time.Time // nil: still open at the end of the period
	Seconds    int64      // only the part inside the period
	Cause      string
	Summary    string
}

// Monthly is everything a monthly report shows for one website. All times are UTC.
type Monthly struct {
	MonitorID uuid.UUID
	Name      string
	URL       string // host and path only: no credentials, no query string
	Month     time.Time
	From, To  time.Time // To is the end of the month, or now for the month in progress
	Partial   bool      // the month is not over yet

	Uptime          Uptime
	DowntimeSeconds int64
	IncidentCount   int
	Incidents       []IncidentRow // at most MaxListedIncidents, oldest first
	Days            []DayStat     // one per day that had checks, oldest first

	GeneratedAt time.Time
}

// HasData says whether any check completed in the period. A website without checks (paused all month, or added after it) gets no report.
func (m Monthly) HasData() bool { return m.Uptime.TotalChecks > 0 }

// MonthStart returns the first instant of t's month in UTC.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// ParseMonth reads "2026-09" as the first instant of that month.
func ParseMonth(s string) (time.Time, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, validation.New("month", "must look like 2026-09")
	}
	return t.UTC(), nil
}

// BuildMonthly gathers one website's month. The month must have started, and must lie inside the history the plan keeps: a report for a month whose
// results were partly deleted would understate the checks and be wrong, so it is refused rather than produced.
func BuildMonthly(ctx context.Context, db DB, orgID, monitorID uuid.UUID, month, now time.Time, retentionDays int) (Monthly, error) {
	now = now.UTC()
	month = MonthStart(month)
	if month.After(now) {
		return Monthly{}, validation.New("month", "that month has not started yet")
	}
	if month.Before(Cutoff(now, retentionDays)) {
		return Monthly{}, validation.New("month", "is older than the history your plan keeps")
	}
	end := month.AddDate(0, 1, 0)
	m := Monthly{MonitorID: monitorID, Month: month, From: month, To: end, GeneratedAt: now}
	if end.After(now) {
		m.To, m.Partial = now, true
	}

	var rawURL string
	err := db.QueryRow(ctx, `SELECT name, url FROM monitors WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, monitorID, orgID).Scan(&m.Name, &rawURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monthly{}, ErrMonitorNotFound
	}
	if err != nil {
		return Monthly{}, err
	}
	m.URL = displayURL(rawURL)

	if m.Uptime, err = ComputeUptime(ctx, db, orgID, &monitorID, Range{From: m.From, To: m.To}); err != nil {
		return Monthly{}, err
	}

	rows, err := db.Query(ctx, `SELECT date_trunc('day', r.checked_at AT TIME ZONE 'UTC'), count(*), count(*) FILTER (WHERE NOT r.success)
		FROM check_results r WHERE r.monitor_id = $1 AND r.checked_at >= $2 AND r.checked_at < $3 AND NOT r.in_maintenance
		GROUP BY 1 ORDER BY 1`, monitorID, m.From, m.To)
	if err != nil {
		return Monthly{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DayStat
		if err := rows.Scan(&d.Day, &d.Checks, &d.Failed); err != nil {
			return Monthly{}, err
		}
		d.Day = d.Day.UTC()
		m.Days = append(m.Days, d)
	}
	if err := rows.Err(); err != nil {
		return Monthly{}, err
	}
	rows.Close()

	// Every outage that overlaps the period, even one that began last month or is still open: only the part inside the period counts.
	irows, err := db.Query(ctx, `SELECT started_at, resolved_at, COALESCE(cause_category, ''), COALESCE(summary, '')
		FROM incidents WHERE monitor_id = $1 AND started_at < $3 AND COALESCE(resolved_at, $3) > $2 ORDER BY started_at`, monitorID, m.From, m.To)
	if err != nil {
		return Monthly{}, err
	}
	defer irows.Close()
	for irows.Next() {
		var in IncidentRow
		if err := irows.Scan(&in.StartedAt, &in.ResolvedAt, &in.Cause, &in.Summary); err != nil {
			return Monthly{}, err
		}
		from, to := in.StartedAt, m.To
		if from.Before(m.From) {
			from = m.From
		}
		if in.ResolvedAt != nil && in.ResolvedAt.Before(to) {
			to = *in.ResolvedAt
		}
		if to.After(from) {
			in.Seconds = int64(to.Sub(from).Seconds())
		}
		m.DowntimeSeconds += in.Seconds
		m.IncidentCount++
		if len(m.Incidents) < MaxListedIncidents {
			m.Incidents = append(m.Incidents, in)
		}
	}
	return m, irows.Err()
}

// displayURL keeps what identifies a website (host and path) and drops what must not be printed on a document that is forwarded: any user name and
// password, and the query string, which can carry tokens.
func displayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}
