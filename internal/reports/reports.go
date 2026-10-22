package reports

import (
	"context"
	"fmt"
	"hawkeye/internal/checks"
	"hawkeye/internal/plans"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DB is satisfied by *pgxpool.Pool and pgx.Tx.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// MinChecksForUptime is the least number of completed checks needed before a percentage is shown.
const MinChecksForUptime = 5

// UptimeMethod names the metric so it is never mistaken for continuous measurement.
const UptimeMethod = "check_based"

// Uptime is check-based availability over a window:
//
//	uptime % = successful checks / completed checks x 100
//
// Only completed checks count. Paused periods, checks cancelled by a shutdown, and periods when a check was
// late or never ran produce no result, so they are outside the denominator. The percentage is truncated
// (never rounded up) to three decimals so a window with failures cannot display as 100%. Below
// MinChecksForUptime completed checks no percentage is given.
type Uptime struct {
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	Clamped          bool      `json:"clamped_to_retention"`
	TotalChecks      int       `json:"total_checks"`
	SuccessfulChecks int       `json:"successful_checks"`
	FailedChecks     int       `json:"failed_checks"`
	UptimePercent    *float64  `json:"uptime_percent"`
	InsufficientData bool      `json:"insufficient_data"`
	MinChecks        int       `json:"min_checks"`
	AvgResponseMs    *int      `json:"avg_response_ms"` // over successful checks
	Method           string    `json:"method"`
}

// Percent returns the truncated uptime percentage, or nil with insufficient=true when there is too little data.
func Percent(successful, total int) (pct *float64, insufficient bool) {
	if total < MinChecksForUptime {
		return nil, true
	}
	p := math.Floor(float64(successful)/float64(total)*100*1000) / 1000
	return &p, false
}

// scope restricts queries to an organization's live monitors, optionally to one monitor.
func scope(orgID uuid.UUID, monitorID *uuid.UUID, r Range) (string, []any) {
	where := `m.organization_id = $1 AND r.checked_at >= $2 AND r.checked_at < $3`
	args := []any{orgID, r.From, r.To}
	if monitorID != nil {
		args = append(args, *monitorID)
		where += fmt.Sprintf(" AND r.monitor_id = $%d", len(args))
	} else {
		where += " AND m.deleted_at IS NULL"
	}
	return where, args
}

// ComputeUptime aggregates check results in the range for a monitor, or for every live monitor of the organization.
func ComputeUptime(ctx context.Context, db DB, orgID uuid.UUID, monitorID *uuid.UUID, r Range) (Uptime, error) {
	where, args := scope(orgID, monitorID, r)
	u := Uptime{From: r.From, To: r.To, Clamped: r.Clamped, MinChecks: MinChecksForUptime, Method: UptimeMethod}
	var avg *float64
	err := db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE r.success),
			avg(r.response_time_ms) FILTER (WHERE r.success AND r.response_time_ms IS NOT NULL)::float8
		FROM check_results r JOIN monitors m ON m.id = r.monitor_id WHERE `+where+` AND NOT r.in_maintenance`, args...).
		Scan(&u.TotalChecks, &u.SuccessfulChecks, &avg)
	if err != nil {
		return Uptime{}, err
	}
	u.FailedChecks = u.TotalChecks - u.SuccessfulChecks
	u.UptimePercent, u.InsufficientData = Percent(u.SuccessfulChecks, u.TotalChecks)
	if avg != nil {
		v := int(math.Round(*avg))
		u.AvgResponseMs = &v
	}
	return u, nil
}

// Point is one aggregated bucket of the response-time series.
type Point struct {
	Time   time.Time `json:"t"`
	AvgMs  *int      `json:"avg_ms"` // over successful checks; nil if the bucket had none
	MaxMs  *int      `json:"max_ms"`
	Checks int       `json:"checks"`
	Failed int       `json:"failed"`
}

// Series returns response times aggregated into buckets (at most MaxPoints), oldest first. Buckets without
// checks are omitted so charts show gaps honestly.
func Series(ctx context.Context, db DB, orgID uuid.UUID, monitorID *uuid.UUID, r Range) ([]Point, int, error) {
	bucket := BucketSeconds(r.To.Sub(r.From))
	where, args := scope(orgID, monitorID, r)
	args = append(args, float64(bucket))
	rows, err := db.Query(ctx, fmt.Sprintf(`SELECT to_timestamp(floor(extract(epoch FROM r.checked_at) / $%[1]d) * $%[1]d) AS t,
			round(avg(r.response_time_ms) FILTER (WHERE r.success AND r.response_time_ms IS NOT NULL))::int,
			max(r.response_time_ms) FILTER (WHERE r.success),
			count(*), count(*) FILTER (WHERE NOT r.success)
		FROM check_results r JOIN monitors m ON m.id = r.monitor_id WHERE %[2]s
		GROUP BY 1 ORDER BY 1`, len(args), where), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Time, &p.AvgMs, &p.MaxMs, &p.Checks, &p.Failed); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, bucket, rows.Err()
}

// Result is one row of check history.
type Result struct {
	ID             int64     `json:"id"`
	CheckedAt      time.Time `json:"checked_at"`
	Success        bool      `json:"success"`
	HTTPStatusCode *int      `json:"http_status_code"`
	ResponseTimeMs *int      `json:"response_time_ms"`
	ErrorCategory  *string   `json:"error_category"`
	ErrorSummary   *string   `json:"error_summary"`
	InMaintenance  bool      `json:"in_maintenance"` // recorded during a maintenance window; not counted in uptime
	Region         string    `json:"region"`         // where the check ran
}

// Results returns one page of a monitor's check history (newest first) and the total in the range.
func Results(ctx context.Context, db DB, orgID, monitorID uuid.UUID, r Range, limit, offset int) ([]Result, int, error) {
	where, args := scope(orgID, &monitorID, r)
	var total int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM check_results r JOIN monitors m ON m.id = r.monitor_id WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.Query(ctx, fmt.Sprintf(`SELECT r.id, r.checked_at, r.success, r.http_status_code, r.response_time_ms, r.error_category, r.error_summary, r.in_maintenance, r.region
		FROM check_results r JOIN monitors m ON m.id = r.monitor_id WHERE %s
		ORDER BY r.checked_at DESC, r.id DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var x Result
		var code *int16
		if err := rows.Scan(&x.ID, &x.CheckedAt, &x.Success, &code, &x.ResponseTimeMs, &x.ErrorCategory, &x.ErrorSummary, &x.InMaintenance, &x.Region); err != nil {
			return nil, 0, err
		}
		if code != nil {
			c := int(*code)
			x.HTTPStatusCode = &c
		}
		out = append(out, x)
	}
	return out, total, rows.Err()
}

// MonitorCounts summarises current monitor states.
type MonitorCounts struct {
	Total   int `json:"total"`
	Up      int `json:"up"`
	Down    int `json:"down"`
	Pending int `json:"pending"` // enabled, no confirmed state yet
	Paused  int `json:"paused"`
}

// Summary is the dashboard headline data.
type Summary struct {
	Monitors      MonitorCounts `json:"monitors"`
	OpenIncidents int           `json:"open_incidents"`
	Uptime        Uptime        `json:"uptime"`
	// Certificates lists up to five enabled HTTPS monitors whose certificate expires within 30 days (or already has), soonest first.
	Certificates []CertificateAlert `json:"certificates"`
	// CertificatesTracked is how many enabled monitors have a known certificate expiry (so the dashboard can say "all good").
	CertificatesTracked int `json:"certificates_tracked"`
	// Domains lists up to five tracked domains whose registration expires within 30 days (or already has), soonest first.
	Domains []DomainAlert `json:"domains"`
	// DomainsTracked is how many domains have a known expiry date.
	DomainsTracked int       `json:"domains_tracked"`
	GeneratedAt    time.Time `json:"generated_at"`
}

// DomainAlert is one domain registration that needs attention soon.
type DomainAlert struct {
	DomainID      uuid.UUID `json:"domain_id"`
	Name          string    `json:"name"`
	ExpiresAt     time.Time `json:"expires_at"`
	DaysRemaining int       `json:"days_remaining"`
	Status        string    `json:"status"` // expiring or expired
}

// CertificateAlert is one certificate that needs attention soon.
type CertificateAlert struct {
	MonitorID     uuid.UUID `json:"monitor_id"`
	MonitorName   string    `json:"monitor_name"`
	ExpiresAt     time.Time `json:"expires_at"`
	DaysRemaining int       `json:"days_remaining"`
	Status        string    `json:"status"` // expiring or expired
}

func DashboardSummary(ctx context.Context, db DB, orgID uuid.UUID, r Range, now time.Time) (Summary, error) {
	s := Summary{GeneratedAt: now.UTC()}
	err := db.QueryRow(ctx, `SELECT count(*),
			count(*) FILTER (WHERE enabled AND current_status = 'up'),
			count(*) FILTER (WHERE enabled AND current_status = 'down'),
			count(*) FILTER (WHERE enabled AND current_status = 'unknown'),
			count(*) FILTER (WHERE NOT enabled)
		FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL`, orgID).
		Scan(&s.Monitors.Total, &s.Monitors.Up, &s.Monitors.Down, &s.Monitors.Pending, &s.Monitors.Paused)
	if err != nil {
		return s, err
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM incidents i JOIN monitors m ON m.id = i.monitor_id
		WHERE m.organization_id = $1 AND m.deleted_at IS NULL AND i.status = 'open'`, orgID).Scan(&s.OpenIncidents); err != nil {
		return s, err
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL AND enabled AND ssl_expires_at IS NOT NULL`,
		orgID).Scan(&s.CertificatesTracked); err != nil {
		return s, err
	}
	s.Certificates = []CertificateAlert{}
	rows, err := db.Query(ctx, `SELECT id, name, ssl_expires_at FROM monitors
		WHERE organization_id = $1 AND deleted_at IS NULL AND enabled AND ssl_expires_at IS NOT NULL AND ssl_expires_at <= $2::timestamptz + interval '30 days'
		ORDER BY ssl_expires_at, id LIMIT 5`, orgID, now)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CertificateAlert
		if err := rows.Scan(&c.MonitorID, &c.MonitorName, &c.ExpiresAt); err != nil {
			return s, err
		}
		c.DaysRemaining, c.Status = checks.SSLDaysRemaining(c.ExpiresAt, now), checks.SSLStatus(c.ExpiresAt, now)
		s.Certificates = append(s.Certificates, c)
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	rows.Close()
	if err := summarizeDomains(ctx, db, orgID, now, &s); err != nil {
		return s, err
	}
	s.Uptime, err = ComputeUptime(ctx, db, orgID, nil, r)
	return s, err
}

// summarizeDomains fills in the domain part of the summary. Only domains within the plan's limit count: the rest are not looked up.
func summarizeDomains(ctx context.Context, db DB, orgID uuid.UUID, now time.Time, s *Summary) error {
	s.Domains = []DomainAlert{}
	p, err := plans.EffectivePlan(ctx, db, orgID)
	if err != nil {
		return err
	}
	const tracked = `SELECT id, name, expires_at FROM (SELECT id, name, status, expires_at, row_number() OVER (ORDER BY created_at, id) AS rank
		FROM domains WHERE organization_id = $1) d WHERE rank <= $2 AND status = 'known'`
	if err := db.QueryRow(ctx, `SELECT count(*) FROM (`+tracked+`) t`, orgID, p.MaxDomains).Scan(&s.DomainsTracked); err != nil {
		return err
	}
	rows, err := db.Query(ctx, tracked+` AND expires_at <= $3::timestamptz + interval '30 days' ORDER BY expires_at, id LIMIT 5`, orgID, p.MaxDomains, now)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d DomainAlert
		if err := rows.Scan(&d.DomainID, &d.Name, &d.ExpiresAt); err != nil {
			return err
		}
		d.DaysRemaining, d.Status = checks.SSLDaysRemaining(d.ExpiresAt, now), checks.SSLStatus(d.ExpiresAt, now)
		s.Domains = append(s.Domains, d)
	}
	return rows.Err()
}
