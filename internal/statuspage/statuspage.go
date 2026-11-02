// Package statuspage manages an organization's public status pages (one or more, by plan) and builds what the public sees.
//
// The public view is deliberately narrow: only monitors the owner added, under a name the owner chose, with their
// current state, daily uptime and recent incident times. Monitor URLs, error messages and causes are never included.
package statuspage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/branding"
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
	ErrNoLogo    = errors.New("this status page has no logo")
	ErrNotFound  = errors.New("status page not found")
	ErrSlugTaken = errors.New("status page address already taken")

	slugRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)
	colorRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)
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

// Config is what the owner edits. ID is set by the server.
type Config struct {
	ID          uuid.UUID       `json:"id"`
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Published   bool            `json:"published"`
	Monitors    []MonitorConfig `json:"monitors"`
	// Branding (Pro and Agency; hiding "Powered by" on Agency). Kept after a downgrade, but only shown while the plan includes it.
	AccentColor   string `json:"accent_color"`    // "#rrggbb", or "" for the standard look
	WebsiteURL    string `json:"website_url"`     // an https link back to the client's own website, or ""
	HidePoweredBy bool   `json:"hide_powered_by"` // hide "Powered by HawkEye"
	HasLogo       bool   `json:"has_logo"`        // read-only: the logo is uploaded separately
	LogoVersion   int64  `json:"logo_version"`    // read-only: changes whenever the logo does, for cache-busting
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

// PublicBranding is the page's look, as far as the plan allows it. Empty fields mean the standard HawkEye look.
type PublicBranding struct {
	LogoURL     string `json:"logo_url,omitempty"`
	AccentColor string `json:"accent_color,omitempty"`
	WebsiteURL  string `json:"website_url,omitempty"`
	PoweredBy   bool   `json:"powered_by"` // show "Powered by HawkEye"
}

type Public struct {
	Branding    PublicBranding `json:"branding"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Overall     string         `json:"overall"`
	Monitors    []MonitorView  `json:"monitors"`
	Incidents   []IncidentView `json:"incidents"`
	HistoryDays int            `json:"history_days"`
	GeneratedAt time.Time      `json:"generated_at"`
}

type Service struct {
	pool  *pgxpool.Pool
	plans *plans.Service
	now   func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	page    Public
	expires time.Time
}

func NewService(pool *pgxpool.Pool, p *plans.Service) *Service {
	return &Service{pool: pool, plans: p, now: time.Now, cache: map[string]cached{}}
}

// List returns the organization's pages, oldest first.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Config, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM status_pages WHERE organization_id = $1 ORDER BY created_at, id`, orgID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := make([]Config, 0, len(ids))
	for _, id := range ids {
		c, err := s.Get(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, nil
}

// Get returns one of the organization's pages. Another organization's page is ErrNotFound.
func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (*Config, error) {
	var c Config
	var logoAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT id, slug, title, description, published, COALESCE(accent_color, ''), COALESCE(website_url, ''), hide_powered_by, logo_updated_at
		FROM status_pages WHERE id = $1 AND organization_id = $2`, id, orgID).
		Scan(&c.ID, &c.Slug, &c.Title, &c.Description, &c.Published, &c.AccentColor, &c.WebsiteURL, &c.HidePoweredBy, &logoAt)
	if logoAt != nil {
		c.HasLogo, c.LogoVersion = true, logoAt.UnixMilli()
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT m.id, COALESCE(pm.display_name, '') FROM status_page_monitors pm
		JOIN monitors m ON m.id = pm.monitor_id WHERE pm.page_id = $1 AND m.deleted_at IS NULL ORDER BY pm.position`, id)
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

// Create adds a page, within the plan's limit of pages.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, in Config) (*Config, error) {
	return s.save(ctx, orgID, nil, in)
}

// Update replaces one of the organization's pages. It works even above the plan's limit (after a downgrade), like editing a monitor.
func (s *Service) Update(ctx context.Context, orgID, id uuid.UUID, in Config) (*Config, error) {
	return s.save(ctx, orgID, &id, in)
}

func (s *Service) save(ctx context.Context, orgID uuid.UUID, id *uuid.UUID, in Config) (*Config, error) {
	in, err := validate(in)
	if err != nil {
		return nil, err
	}
	var pageID uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialise per organization so two creations cannot both pass the limit check.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
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
		var cur Config // the stored branding, so a page saved again after a downgrade keeps what it had
		if id != nil {
			err := tx.QueryRow(ctx, `SELECT COALESCE(accent_color, ''), COALESCE(website_url, ''), hide_powered_by FROM status_pages
				WHERE id = $1 AND organization_id = $2 FOR UPDATE`, *id, orgID).Scan(&cur.AccentColor, &cur.WebsiteURL, &cur.HidePoweredBy)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
		}
		if err := s.checkBranding(ctx, tx, orgID, cur, in); err != nil {
			return err
		}
		if id == nil {
			if err := s.plans.CanCreateStatusPage(ctx, tx, orgID); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `INSERT INTO status_pages (organization_id, slug, title, description, published, accent_color, website_url, hide_powered_by)
				VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8) RETURNING id`,
				orgID, in.Slug, in.Title, in.Description, in.Published, in.AccentColor, in.WebsiteURL, in.HidePoweredBy).Scan(&pageID); err != nil {
				return err
			}
		} else {
			if err := tx.QueryRow(ctx, `UPDATE status_pages SET slug = $3, title = $4, description = $5, published = $6,
					accent_color = NULLIF($7, ''), website_url = NULLIF($8, ''), hide_powered_by = $9
				WHERE id = $1 AND organization_id = $2 RETURNING id`,
				*id, orgID, in.Slug, in.Title, in.Description, in.Published, in.AccentColor, in.WebsiteURL, in.HidePoweredBy).Scan(&pageID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM status_page_monitors WHERE page_id = $1`, pageID); err != nil {
				return err
			}
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
	return s.Get(ctx, orgID, pageID)
}

// checkBranding refuses branding the plan does not include. Only a change counts: clearing is always allowed, and values already stored (kept after a
// downgrade) may be saved again unchanged, so editing the title of such a page still works.
func (s *Service) checkBranding(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, cur, in Config) error {
	if (in.AccentColor != "" && in.AccentColor != cur.AccentColor) || (in.WebsiteURL != "" && in.WebsiteURL != cur.WebsiteURL) {
		if err := s.plans.CanBrandStatusPages(ctx, tx, orgID); err != nil {
			return err
		}
	}
	if in.HidePoweredBy && !cur.HidePoweredBy {
		return s.plans.CanWhiteLabelStatusPages(ctx, tx, orgID)
	}
	return nil
}

// SaveLogo validates, cleans and stores a page's logo, replacing any earlier one. The plan must include status page branding.
func (s *Service) SaveLogo(ctx context.Context, orgID, id uuid.UUID, raw []byte) error {
	if err := s.plans.CanBrandStatusPages(ctx, s.pool, orgID); err != nil {
		return err
	}
	logo, err := branding.NormalizeLogo(raw)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE status_pages SET logo = $3, logo_type = $4, logo_width = $5, logo_height = $6, logo_updated_at = now()
		WHERE id = $1 AND organization_id = $2`, id, orgID, logo.Data, logo.Type, logo.Width, logo.Height)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.forget()
	return nil
}

// DeleteLogo removes a page's logo; it succeeds when there is none. Allowed on every plan.
func (s *Service) DeleteLogo(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE status_pages SET logo = NULL, logo_type = NULL, logo_width = NULL, logo_height = NULL, logo_updated_at = NULL
		WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.forget()
	return nil
}

// Logo returns a page's stored logo for the owner's preview (whatever the plan). ErrNoLogo when there is none.
func (s *Service) Logo(ctx context.Context, orgID, id uuid.UUID) ([]byte, string, error) {
	return s.logo(ctx, `SELECT logo, logo_type FROM status_pages WHERE id = $1 AND organization_id = $2`, id, orgID)
}

// PublicLogo returns the logo of a published page whose plan includes branding; anything else is ErrNoLogo, so the public endpoint reveals
// nothing about drafts or about logos kept after a downgrade.
func (s *Service) PublicLogo(ctx context.Context, slug string) ([]byte, string, error) {
	if !slugRE.MatchString(slug) {
		return nil, "", ErrNoLogo
	}
	var id, orgID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id, organization_id FROM status_pages WHERE slug = $1 AND published`, slug).Scan(&id, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNoLogo
	}
	if err != nil {
		return nil, "", err
	}
	plan, err := plans.EffectivePlan(ctx, s.pool, orgID)
	if err != nil {
		return nil, "", err
	}
	if !plan.StatusPageBranding {
		return nil, "", ErrNoLogo
	}
	return s.Logo(ctx, orgID, id)
}

func (s *Service) logo(ctx context.Context, q string, args ...any) ([]byte, string, error) {
	var data []byte
	var typ *string
	err := s.pool.QueryRow(ctx, q, args...).Scan(&data, &typ)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && typ == nil {
		return nil, "", ErrNoLogo
	}
	if err != nil {
		return nil, "", err
	}
	return data, "image/" + *typ, nil
}

// Delete removes one of the organization's pages (its address becomes available again).
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM status_pages WHERE id = $1 AND organization_id = $2`, id, orgID)
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
	in.AccentColor = strings.ToLower(strings.TrimSpace(in.AccentColor))
	if in.AccentColor != "" && !colorRE.MatchString(in.AccentColor) {
		bad["accent_color"] = "must be a colour like #1a73e8"
	}
	in.WebsiteURL = strings.TrimSpace(in.WebsiteURL)
	if in.WebsiteURL != "" {
		u, err := url.Parse(in.WebsiteURL)
		switch {
		case err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || len(in.WebsiteURL) > 2048:
			bad["website_url"] = "must be an https:// web address, like https://www.example.com"
		default:
			in.WebsiteURL = u.String()
		}
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
	var look Config
	var logoAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT id, organization_id, title, description, COALESCE(accent_color, ''), COALESCE(website_url, ''), hide_powered_by, logo_updated_at
		FROM status_pages WHERE slug = $1 AND published`, slug).
		Scan(&pageID, &orgID, &p.Title, &p.Description, &look.AccentColor, &look.WebsiteURL, &look.HidePoweredBy, &logoAt)
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
	p.Branding = PublicBranding{PoweredBy: !(look.HidePoweredBy && plan.StatusPageWhiteLabel)}
	if plan.StatusPageBranding {
		p.Branding.AccentColor, p.Branding.WebsiteURL = look.AccentColor, look.WebsiteURL
		if logoAt != nil {
			p.Branding.LogoURL = fmt.Sprintf("/api/v1/public/status/%s/logo?v=%d", slug, logoAt.UnixMilli())
		}
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
