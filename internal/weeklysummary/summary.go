package weeklysummary

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
	"hawkeye/internal/notifications"
	"hawkeye/internal/reports"
)

const (
	maxSites    = 10                  // sites listed by name; the rest are summed up in a line
	maxSlowest  = 3                   // slowest sites mentioned
	expiryAhead = 30 * 24 * time.Hour // certificates and domains expiring within this are mentioned
	maxExpiring = 5
)

// Site is one monitor's week.
type Site struct {
	Name      string
	Uptime    *float64 // nil with too few checks (or none: paused all week)
	Incidents int
	AvgMs     *int // over successful checks
}

// Expiry is a certificate or domain registration that runs out soon (or already has).
type Expiry struct {
	Kind string // "certificate" or "domain"
	Name string
	At   time.Time
}

// Summary is one organization's week.
type Summary struct {
	OrgName   string
	From, To  time.Time // the week: Monday 00:00 UTC to the next Monday
	Uptime    *float64  // across every check of every monitor, check-based like everywhere in HawkEye
	Incidents int
	Downtime  time.Duration
	Sites     []Site // worst first
	Slowest   []Site
	Expiring  []Expiry
}

// Build gathers an organization's week from check results, incidents, certificates and domains. now is when the email is sent (for "expires in").
func Build(ctx context.Context, db *pgxpool.Pool, orgID uuid.UUID, orgName string, from, to, now time.Time) (Summary, error) {
	s := Summary{OrgName: orgName, From: from, To: to}
	rows, err := db.Query(ctx, `SELECT m.id, m.name, m.ssl_expires_at,
			count(r.id), count(r.id) FILTER (WHERE r.success),
			avg(r.response_time_ms) FILTER (WHERE r.success AND r.response_time_ms IS NOT NULL)::float8
		FROM monitors m
		LEFT JOIN check_results r ON r.monitor_id = m.id AND r.checked_at >= $2 AND r.checked_at < $3 AND NOT r.in_maintenance
		WHERE m.organization_id = $1 AND m.deleted_at IS NULL
		GROUP BY m.id ORDER BY m.name, m.id`, orgID, from, to)
	if err != nil {
		return s, err
	}
	index := map[uuid.UUID]int{}
	var ids []uuid.UUID
	total, ok := 0, 0
	for rows.Next() {
		var id uuid.UUID
		var site Site
		var ssl *time.Time
		var n, good int
		var avg *float64
		if err := rows.Scan(&id, &site.Name, &ssl, &n, &good, &avg); err != nil {
			rows.Close()
			return s, err
		}
		site.Uptime, _ = reports.Percent(good, n)
		if avg != nil {
			v := int(*avg + 0.5)
			site.AvgMs = &v
		}
		total, ok = total+n, ok+good
		if ssl != nil && ssl.Before(now.Add(expiryAhead)) {
			s.Expiring = append(s.Expiring, Expiry{Kind: "certificate", Name: site.Name, At: *ssl})
		}
		index[id] = len(s.Sites)
		ids = append(ids, id)
		s.Sites = append(s.Sites, site)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}
	s.Uptime, _ = reports.Percent(ok, total)

	// Incidents that overlapped the week, and how much of the week they took.
	ir, err := db.Query(ctx, `SELECT monitor_id, count(*),
			COALESCE(sum(EXTRACT(EPOCH FROM LEAST(COALESCE(resolved_at, $3), $3) - GREATEST(started_at, $2))), 0)::float8
		FROM incidents WHERE monitor_id = ANY($1) AND started_at < $3 AND COALESCE(resolved_at, $3) > $2 GROUP BY monitor_id`, ids, from, to)
	if err != nil {
		return s, err
	}
	for ir.Next() {
		var id uuid.UUID
		var n int
		var secs float64
		if err := ir.Scan(&id, &n, &secs); err != nil {
			ir.Close()
			return s, err
		}
		s.Sites[index[id]].Incidents = n
		s.Incidents += n
		s.Downtime += time.Duration(secs * float64(time.Second))
	}
	ir.Close()
	if err := ir.Err(); err != nil {
		return s, err
	}

	dr, err := db.Query(ctx, `SELECT name, expires_at FROM domains WHERE organization_id = $1 AND status = 'known' AND expires_at < $2
		ORDER BY expires_at LIMIT $3`, orgID, now.Add(expiryAhead), maxExpiring)
	if err != nil {
		return s, err
	}
	for dr.Next() {
		var e Expiry
		e.Kind = "domain"
		if err := dr.Scan(&e.Name, &e.At); err != nil {
			dr.Close()
			return s, err
		}
		s.Expiring = append(s.Expiring, e)
	}
	dr.Close()
	if err := dr.Err(); err != nil {
		return s, err
	}
	sort.SliceStable(s.Expiring, func(i, j int) bool { return s.Expiring[i].At.Before(s.Expiring[j].At) })
	if len(s.Expiring) > maxExpiring {
		s.Expiring = s.Expiring[:maxExpiring]
	}

	for _, site := range s.Sites {
		if site.AvgMs != nil {
			s.Slowest = append(s.Slowest, site)
		}
	}
	sort.SliceStable(s.Slowest, func(i, j int) bool { return *s.Slowest[i].AvgMs > *s.Slowest[j].AvgMs })
	if len(s.Slowest) > maxSlowest {
		s.Slowest = s.Slowest[:maxSlowest]
	}
	// Worst first: lowest uptime, then most incidents; sites with no data last.
	sort.SliceStable(s.Sites, func(i, j int) bool {
		a, b := s.Sites[i], s.Sites[j]
		switch {
		case a.Uptime == nil || b.Uptime == nil:
			return a.Uptime != nil && b.Uptime == nil
		case *a.Uptime != *b.Uptime:
			return *a.Uptime < *b.Uptime
		}
		return a.Incidents > b.Incidents
	})
	return s, nil
}

func pct(p *float64) string {
	if p == nil {
		return ""
	}
	if *p >= 100 {
		return "100%"
	}
	return fmt.Sprintf("%.2f%%", float64(int(*p*100))/100) // truncated, never rounded up
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func weekLabel(from, to time.Time) string {
	last := to.Add(-24 * time.Hour)
	if from.Year() != last.Year() {
		return from.Format("2 Jan 2006") + " – " + last.Format("2 Jan 2006")
	}
	return from.Format("2 Jan") + " – " + last.Format("2 Jan 2006")
}

// Subject is the email's subject line: the week's headline in a few words.
func (s Summary) Subject() string {
	if s.Uptime == nil {
		return "Your week on HawkEye"
	}
	if s.Incidents == 0 {
		return "Your week on HawkEye: " + pct(s.Uptime) + " uptime, no incidents"
	}
	return "Your week on HawkEye: " + pct(s.Uptime) + " uptime, " + plural(s.Incidents, "incident", "incidents")
}

// Content renders the email. Every name in it is customer text; email.Content escapes it and keeps it to one line.
func (s Summary) Content(appBaseURL string, now time.Time) email.Content {
	n := len(s.Sites)
	c := email.Content{
		AppBaseURL:  appBaseURL,
		Heading:     s.OrgName + " · " + weekLabel(s.From, s.To),
		ButtonLabel: "Open your dashboard", ButtonURL: appBaseURL + "/dashboard?utm_source=hawkeye&utm_medium=email&utm_campaign=weekly_summary",
	}
	switch {
	case s.Uptime == nil:
		c.Preheader = "Not enough checks last week to measure uptime."
		c.Paragraphs = []string{fmt.Sprintf("Your %s had too few checks last week to measure uptime, for example because they were paused.", plural(n, "website", "websites"))}
	case s.Incidents == 0:
		c.Preheader = "Everything stayed up last week."
		c.Paragraphs = []string{fmt.Sprintf("A quiet week: across your %s, uptime was %s and there were no incidents.", plural(n, "website", "websites"), pct(s.Uptime))}
	default:
		c.Preheader = fmt.Sprintf("%s uptime, %s last week.", pct(s.Uptime), plural(s.Incidents, "incident", "incidents"))
		c.Paragraphs = []string{fmt.Sprintf("Across your %s, uptime was %s. There %s, with %s of downtime in total.",
			plural(n, "website", "websites"), pct(s.Uptime), map[bool]string{true: "was 1 incident", false: fmt.Sprintf("were %d incidents", s.Incidents)}[s.Incidents == 1],
			notifications.Duration(s.Downtime))}
	}

	shown := s.Sites
	if len(shown) > maxSites {
		shown = shown[:maxSites]
	}
	for _, site := range shown {
		v := "No checks this week"
		if site.Uptime != nil {
			v = pct(site.Uptime) + " uptime"
		}
		if site.Incidents > 0 {
			v += " · " + plural(site.Incidents, "incident", "incidents")
		}
		c.Facts = append(c.Facts, email.Fact{Label: site.Name, Value: v})
	}
	if rest := n - len(shown); rest > 0 {
		c.Paragraphs = append(c.Paragraphs, fmt.Sprintf("The %d least available are listed below; %d more %s on your dashboard.", len(shown), rest,
			map[bool]string{true: "is", false: "are"}[rest == 1]))
	}

	// After the sites, in the same list: the slowest, then what is about to expire.
	if len(s.Slowest) > 0 {
		parts := make([]string, len(s.Slowest))
		for i, site := range s.Slowest {
			parts[i] = fmt.Sprintf("%s (%s)", site.Name, notifications.Milliseconds(*site.AvgMs))
		}
		label := "Slowest on average"
		if len(parts) == 1 {
			label = "Average response time"
		}
		c.Facts = append(c.Facts, email.Fact{Label: label, Value: strings.Join(parts, ", ")})
	}
	for _, e := range s.Expiring {
		label := "SSL certificate"
		if e.Kind == "domain" {
			label = "Domain"
		}
		when := "expires in " + daysLeft(e.At, now) + " (" + e.At.UTC().Format("2 Jan") + ")"
		if !e.At.After(now) {
			when = "has expired"
		}
		c.Facts = append(c.Facts, email.Fact{Label: label, Value: e.Name + " " + when})
	}
	c.Note = "You get this summary every Monday as a member of " + s.OrgName + " on HawkEye. Turn it off under Account → Email preferences, or with the link below. Alerts are not affected."
	return c
}

func daysLeft(at, now time.Time) string {
	d := int(at.Sub(now).Hours() / 24)
	if d < 1 {
		return "less than a day"
	}
	return plural(d, "day", "days")
}
