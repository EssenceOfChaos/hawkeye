// Package statuspage manages an organization's public status page and builds what the public sees.
//
// The public view is deliberately narrow: only monitors the owner added, under a name the owner chose, with their
// current state, daily uptime and recent incident times. Monitor URLs, error messages and causes are never included.
package statuspage

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/reports"
	"hawkeye/internal/validation"
)

const (
	MaxMonitors   = 50
	MaxHistory    = 90 // days of daily bars, further limited by the plan's retention
	IncidentDays  = 14
	maxIncidents  = 50
	cacheTTL      = 30 * time.Second
	maxCacheItems = 2000
)

var (
	ErrNotFound  = errors.New("status page not found")
	ErrSlugTaken = errors.New("status page address already taken")

	slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)
)

// Overall states of a page.
const (
	Operational = "operational"
	Partial     = "partial_outage"
	Major       = "major_outage"
	Maintenance = "maintenance"
)

// Monitor states on the public page.
const (
	StateUp          = "up"
	StateDown        = "down"
	StateMaintenance = "maintenance"
	StateUnknown     = "unknown" // paused, or no check yet
)

// Config is what the owner edits.
type Config struct {
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Published   bool            `json:"published"`
	Monitors    []MonitorConfig `json:"monitors"`
}

type MonitorConfig struct {
	MonitorID   uuid.UUID `json:"monitor_id"`
	DisplayName string    `json:"display_name"` // empty means the monitor's own name
}

type Day struct {
	Date          string   `json:"date"` // YYYY-MM-DD, UTC
	UptimePercent *float64 `json:"uptime_percent"`
	Status        string   `json:"status"` // ok, degraded, down, or none when there were too few checks
}

type MonitorView struct {
	Name          string   `json:"name"`
	State         string   `json:"state"`
	UptimePercent *float64 `json:"uptime_percent"` // over the whole period shown
	Days          []Day    `json:"days"`
}

type IncidentView struct {
	Monitor    string     `json:"monitor"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

type Public struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Overall     string         `json:"overall"`
	Monitors    []MonitorView  `json:"monitors"`
	Incidents   []IncidentView `json:"incidents"`
	HistoryDays int            `json:"history_days"`
	GeneratedAt time.Time      `json:"generated_at"`
}

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	page    Public
	expires time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now, cache: map[string]cached{}}
}

// Get returns the organization's page configuration, or nil when it has none.
func (s *Service) Get(ctx context.Context, orgID uuid.UUID) (*Config, error) {
	var c Config
	var pageID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id, slug, title, description, published FROM status_pages WHERE organization_id = $1`, orgID).
		Scan(&pageID, &c.Slug, &c.Title, &c.Description, &c.Published)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT m.id, COALESCE(pm.display_name, '') FROM status_page_monitors pm
		JOIN monitors m ON m.id = pm.monitor_id WHERE pm.page_id = $1 AND m.deleted_at IS NULL ORDER BY pm.position`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	c.Monitors = []MonitorConfig{}
	for rows.Next() {
		var m MonitorConfig
		if err := rows.Scan(&m.MonitorID, &m.DisplayName); err != nil {
			return nil, err
		}
		c.Monitors = append(c.Monitors, m)
	}
	return &c, rows.Err()
}

// Put creates or replaces the organization's page.
func (s *Service) Put(ctx context.Context, orgID uuid.UUID, in Config) (*Config, error) {
	in, err := validate(in)
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ids := make([]uuid.UUID, len(in.Monitors))
		for i, m := range in.Monitors {
			ids[i] = m.MonitorID
		}
		var found int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL AND id = ANY($2)`, orgID, ids).Scan(&found); err != nil {
			return err
		}
		if found != len(ids) {
			return validation.New("monitors", "contains a monitor that does not exist")
		}
		var pageID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO status_pages (organization_id, slug, title, description, published) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (organization_id) DO UPDATE SET slug = EXCLUDED.slug, title = EXCLUDED.title, description = EXCLUDED.description, published = EXCLUDED.published
			RETURNING id`, orgID, in.Slug, in.Title, in.Description, in.Published).Scan(&pageID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM status_page_monitors WHERE page_id = $1`, pageID); err != nil {
			return err
		}
		for i, m := range in.Monitors {
			if _, err := tx.Exec(ctx, `INSERT INTO status_page_monitors (page_id, monitor_id, display_name, position) VALUES ($1, $2, NULLIF($3, ''), $4)`,
				pageID, m.MonitorID, m.DisplayName, i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "slug") {
			return nil, ErrSlugTaken
		}
		return nil, err
	}
	s.forget()
	return s.Get(ctx, orgID)
}

// Delete removes the organization's page (its address becomes available again).
func (s *Service) Delete(ctx context.Context, orgID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM status_pages WHERE organization_id = $1`, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.forget()
	return nil
}

func (s *Service) forget() {
	s.mu.Lock()
	clear(s.cache)
	s.mu.Unlock()
}

func validate(in Config) (Config, error) {
	bad := map[string]string{}
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	if !slugRE.MatchString(in.Slug) {
		bad["slug"] = "must be 3 to 40 letters, numbers or hyphens, and start and end with a letter or number"
	}
	in.Title = strings.TrimSpace(in.Title)
	if n := len([]rune(in.Title)); n < 1 || n > 100 {
		bad["title"] = "must be 1 to 100 characters"
	}
	in.Description = strings.TrimSpace(in.Description)
	if len([]rune(in.Description)) > 500 {
		bad["description"] = "must be at most 500 characters"
	}
	if len(in.Monitors) > MaxMonitors {
		bad["monitors"] = "too many monitors"
	}
	seen := map[uuid.UUID]bool{}
	for i := range in.Monitors {
		m := &in.Monitors[i]
		m.DisplayName = strings.TrimSpace(m.DisplayName)
		if len([]rune(m.DisplayName)) > 100 {
			bad["monitors"] = "display names must be at most 100 characters"
		}
		if seen[m.MonitorID] {
			bad["monitors"] = "lists a monitor twice"
		}
		seen[m.MonitorID] = true
	}
	if in.Monitors == nil {
		in.Monitors = []MonitorConfig{}
	}
	if in.Published && len(in.Monitors) == 0 {
		bad["monitors"] = "add at least one monitor before publishing"
	}
	if len(bad) > 0 {
		return in, &validation.Error{Fields: bad}
	}
	return in, nil
}

// dayStatus classifies a day's uptime for the bars.
func dayStatus(pct *float64) string {
	switch {
	case pct == nil:
		return "none"
	case *pct >= 99.5:
		return "ok"
	case *pct >= 95:
		return "degraded"
	}
	return "down"
}

// overall summarises the monitors: maintenance only when every monitor is under maintenance.
func overall(states []string) string {
	down, maint, known := 0, 0, 0
	for _, st := range states {
		switch st {
		case StateDown:
			down++
			known++
		case StateMaintenance:
			maint++
		case StateUp:
			known++
		}
	}
	switch {
	case down > 0 && down == known && maint == 0:
		return Major
	case down > 0:
		return Partial
	case maint > 0 && known == 0:
		return Maintenance
	}
	return Operational
}

// Public builds the public view for a slug. Unknown and unpublished pages are indistinguishable (ErrNotFound).
// Results are cached briefly so a popular page cannot turn into database load.
func (s *Service) Public(ctx context.Context, slug string) (Public, error) {
	now := s.now()
	s.mu.Lock()
	if c, ok := s.cache[slug]; ok && now.Before(c.expires) {
		s.mu.Unlock()
		return c.page, nil
	}
	s.mu.Unlock()

	p, err := s.build(ctx, slug, now)
	if err != nil {
		return Public{}, err
	}
	s.mu.Lock()
	if len(s.cache) >= maxCacheItems {
		clear(s.cache)
	}
	s.cache[slug] = cached{page: p, expires: now.Add(cacheTTL)}
	s.mu.Unlock()
	return p, nil
}

func (s *Service) build(ctx context.Context, slug string, now time.Time) (Public, error) {
	if !slugRE.MatchString(slug) {
		return Public{}, ErrNotFound
	}
	var p Public
	var pageID, orgID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id, organization_id, title, description FROM status_pages WHERE slug = $1 AND published`, slug).
		Scan(&pageID, &orgID, &p.Title, &p.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return Public{}, ErrNotFound
	}
	if err != nil {
		return Public{}, err
	}
	plan, err := plans.EffectivePlan(ctx, s.pool, orgID)
	if err != nil {
		return Public{}, err
	}
	p.HistoryDays = min(MaxHistory, plan.HistoryRetentionDays)
	p.GeneratedAt = now.UTC().Truncate(time.Second)

	rows, err := s.pool.Query(ctx, `SELECT m.id, COALESCE(pm.display_name, m.name), m.enabled, m.current_status, in_maintenance(m.organization_id, m.id, $2)
		FROM status_page_monitors pm JOIN monitors m ON m.id = pm.monitor_id
		WHERE pm.page_id = $1 AND m.deleted_at IS NULL ORDER BY pm.position`, pageID, now)
	if err != nil {
		return Public{}, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	names := map[uuid.UUID]string{}
	index := map[uuid.UUID]int{}
	p.Monitors = []MonitorView{}
	for rows.Next() {
		var id uuid.UUID
		var name, status string
		var enabled, maint bool
		if err := rows.Scan(&id, &name, &enabled, &status, &maint); err != nil {
			return Public{}, err
		}
		state := StateUnknown
		switch {
		case !enabled:
		case maint:
			state = StateMaintenance
		case status == "up":
			state = StateUp
		case status == "down":
			state = StateDown
		}
		ids = append(ids, id)
		names[id] = name
		index[id] = len(p.Monitors)
		p.Monitors = append(p.Monitors, MonitorView{Name: name, State: state, Days: []Day{}})
	}
	if err := rows.Err(); err != nil {
		return Public{}, err
	}
	rows.Close()

	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -(p.HistoryDays - 1))
	type tally struct{ total, ok int }
	byDay := map[uuid.UUID]map[string]tally{}
	totals := map[uuid.UUID]tally{}
	dr, err := s.pool.Query(ctx, `SELECT monitor_id, to_char(checked_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), count(*), count(*) FILTER (WHERE success)
		FROM check_results WHERE monitor_id = ANY($1) AND checked_at >= $2 AND NOT in_maintenance GROUP BY 1, 2`, ids, from)
	if err != nil {
		return Public{}, err
	}
	defer dr.Close()
	for dr.Next() {
		var id uuid.UUID
		var day string
		var t tally
		if err := dr.Scan(&id, &day, &t.total, &t.ok); err != nil {
			return Public{}, err
		}
		if byDay[id] == nil {
			byDay[id] = map[string]tally{}
		}
		byDay[id][day] = t
		all := totals[id]
		all.total, all.ok = all.total+t.total, all.ok+t.ok
		totals[id] = all
	}
	if err := dr.Err(); err != nil {
		return Public{}, err
	}
	dr.Close()
	states := make([]string, len(p.Monitors))
	for id, i := range index {
		v := &p.Monitors[i]
		for d := from; !d.After(today); d = d.AddDate(0, 0, 1) {
			key := d.Format("2006-01-02")
			t := byDay[id][key]
			pct, _ := reports.Percent(t.ok, t.total)
			v.Days = append(v.Days, Day{Date: key, UptimePercent: pct, Status: dayStatus(pct)})
		}
		v.UptimePercent, _ = reports.Percent(totals[id].ok, totals[id].total)
		states[i] = v.State
	}
	p.Overall = overall(states)

	ir, err := s.pool.Query(ctx, `SELECT monitor_id, started_at, resolved_at FROM incidents
		WHERE monitor_id = ANY($1) AND (resolved_at IS NULL OR resolved_at >= $2) ORDER BY started_at DESC LIMIT $3`,
		ids, now.AddDate(0, 0, -IncidentDays), maxIncidents)
	if err != nil {
		return Public{}, err
	}
	defer ir.Close()
	p.Incidents = []IncidentView{}
	for ir.Next() {
		var id uuid.UUID
		var v IncidentView
		if err := ir.Scan(&id, &v.StartedAt, &v.ResolvedAt); err != nil {
			return Public{}, err
		}
		v.Monitor = names[id]
		p.Incidents = append(p.Incidents, v)
	}
	return p, ir.Err()
}
