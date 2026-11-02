// Package badges draws the public uptime badges a customer can embed on a website or in a README: a large one (400 × 84) and a small one (200 × 28).
//
// A badge is opt-in per monitor and is addressed by a random token, never by the monitor's id, so turning it off makes its address stop working and
// nothing about other monitors can be guessed. It shows only the uptime percentage, the period and whether the monitor is up, down or unknown: never
// the monitor's name or address, so no private detail can leak and no customer text ever reaches the SVG.
package badges

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"math"
	"regexp"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/reports"
)

// Styles.
const (
	Large = "large"
	Small = "small"
)

// States, which pick the icon and colours.
const (
	StateUp      = "up"
	StateDown    = "down"
	StateUnknown = "unknown" // paused, in a maintenance window, not checked yet, or too little data
)

const (
	// MaxPeriodDays is the period a badge reports over, shortened to the plan's history (Free keeps 7 days, so its badges say "Last 7 days").
	MaxPeriodDays = 30
	cacheTTL      = time.Minute
	maxCacheItems = 5000
)

var (
	ErrNotFound = errors.New("badge not found")
	tokenRE     = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

	//go:embed templates/*.svg
	files     embed.FS
	templates = template.Must(template.ParseFS(files, "templates/*.svg"))
)

// View is what a badge shows. Every field is produced by this package; Render escapes them anyway.
type View struct {
	State      string
	Percent    string // "99.98%", "100%", or "" when there are too few checks to say
	PeriodDays int
}

type rendered struct {
	View
	Title, Description, Caption, ShortPeriod string
}

// FormatPercent shows uptime with at most two decimals, truncated like everywhere else in HawkEye (so 99.999 is "99.99%", never "100%").
func FormatPercent(p float64) string {
	if p >= 100 {
		return "100%"
	}
	return fmt.Sprintf("%.2f%%", math.Floor(p*100)/100)
}

// Render draws a badge as SVG.
func Render(style string, v View) ([]byte, error) {
	period := fmt.Sprintf("Last %d days", v.PeriodDays)
	pct := v.Percent
	r := rendered{View: v, ShortPeriod: fmt.Sprintf("%dd", v.PeriodDays)}
	spoken := fmt.Sprintf("%s percent uptime over the last %d days", trimPercent(pct), v.PeriodDays)
	switch {
	case pct == "":
		r.State = StateUnknown
		r.Title, r.Caption = "HawkEye status unknown badge", "Not enough data yet"
		r.Description = "Not enough data yet to determine uptime. Status unknown. Monitored by HawkEye."
	case v.State == StateUp:
		r.Title, r.Caption = "HawkEye uptime badge", period
		r.Description = spoken + ". Monitored by HawkEye."
	case v.State == StateDown:
		r.Title, r.Caption = "HawkEye outage badge", "Service currently down"
		r.Description = spoken + ". Service is currently down. Monitored by HawkEye."
	default:
		r.State = StateUnknown
		r.Title, r.Caption = "HawkEye status unknown badge", "Status unknown · "+period
		r.Description = spoken + ". Current status unknown. Monitored by HawkEye."
	}
	for _, s := range []*string{&r.Percent, &r.Title, &r.Description, &r.Caption, &r.ShortPeriod, &r.State} {
		*s = html.EscapeString(*s)
	}
	name := "large.svg"
	if style == Small {
		name = "small.svg"
	}
	var b bytes.Buffer
	if err := templates.ExecuteTemplate(&b, name, r); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func trimPercent(p string) string {
	if len(p) > 0 && p[len(p)-1] == '%' {
		return p[:len(p)-1]
	}
	return p
}

// Service turns badges on and off and serves them.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	svg     []byte
	expires time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now, cache: map[string]cached{}}
}

func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system CSPRNG failing is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Enable gives a monitor a public badge and returns its token. A monitor that already has one keeps it, so embedded badges keep working.
func (s *Service) Enable(ctx context.Context, orgID, monitorID uuid.UUID) (string, error) {
	var token string
	err := s.pool.QueryRow(ctx, `UPDATE monitors SET badge_token = COALESCE(badge_token, $3)
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL RETURNING badge_token`, monitorID, orgID, newToken()).Scan(&token)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return token, err
}

// Disable removes a monitor's badge: its address stops working at once (and, within a minute, on every API process).
func (s *Service) Disable(ctx context.Context, orgID, monitorID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE monitors SET badge_token = NULL WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, monitorID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.mu.Lock()
	clear(s.cache)
	s.mu.Unlock()
	return nil
}

// Badge draws the badge for a token. Unknown, malformed and revoked tokens, and deleted monitors, are all ErrNotFound. Results are cached briefly, so
// a badge on a busy page costs the database almost nothing.
func (s *Service) Badge(ctx context.Context, token, style string) ([]byte, error) {
	if !tokenRE.MatchString(token) {
		return nil, ErrNotFound
	}
	if style != Small {
		style = Large
	}
	key, now := token+"|"+style, s.now()
	s.mu.Lock()
	if c, ok := s.cache[key]; ok && now.Before(c.expires) {
		s.mu.Unlock()
		return c.svg, nil
	}
	s.mu.Unlock()

	v, err := s.view(ctx, token, now)
	if err != nil {
		return nil, err
	}
	svg, err := Render(style, v)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.cache) >= maxCacheItems {
		clear(s.cache)
	}
	s.cache[key] = cached{svg: svg, expires: now.Add(cacheTTL)}
	s.mu.Unlock()
	return svg, nil
}

func (s *Service) view(ctx context.Context, token string, now time.Time) (View, error) {
	var id, orgID uuid.UUID
	var enabled, maint bool
	var status string
	err := s.pool.QueryRow(ctx, `SELECT id, organization_id, enabled, current_status, in_maintenance(organization_id, id, $2)
		FROM monitors WHERE badge_token = $1 AND deleted_at IS NULL`, token, now).Scan(&id, &orgID, &enabled, &status, &maint)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	plan, err := plans.EffectivePlan(ctx, s.pool, orgID)
	if err != nil {
		return View{}, err
	}
	// 30 days, or 7 on a plan that keeps less history than that (Free): only periods the reports know.
	days := MaxPeriodDays
	if plan.HistoryRetentionDays < MaxPeriodDays {
		days = 7
	}
	r, err := reports.ResolveRange(now, fmt.Sprintf("%dd", days), nil, nil, plan.HistoryRetentionDays)
	if err != nil {
		return View{}, err
	}
	u, err := reports.ComputeUptime(ctx, s.pool, orgID, &id, r)
	if err != nil {
		return View{}, err
	}
	v := View{State: StateUnknown, PeriodDays: days}
	if u.UptimePercent != nil {
		v.Percent = FormatPercent(*u.UptimePercent)
	}
	switch {
	case !enabled || maint:
	case status == "up":
		v.State = StateUp
	case status == "down":
		v.State = StateDown
	}
	return v, nil
}
