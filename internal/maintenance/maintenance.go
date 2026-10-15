// Package maintenance manages maintenance windows: planned periods during which a monitor's checks keep running but
// raise no alerts and do not count against uptime. The rule that decides whether a monitor is covered lives in the
// database (in_maintenance, migration 000013) so the check recorder, the monitor listing and the windows listing agree.
package maintenance

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/validation"
)

const (
	MaxWindowsPerOrganization = 50
	MaxDurationMinutes        = 7 * 24 * 60
)

// Repeat values.
const (
	RepeatNone   = "none"
	RepeatDaily  = "daily"
	RepeatWeekly = "weekly"
)

// Window statuses.
const (
	StatusActive   = "active"
	StatusUpcoming = "upcoming"
	StatusEnded    = "ended" // a one-off window that is over
)

var (
	ErrNotFound = errors.New("maintenance window not found")
	ErrTooMany  = errors.New("too many maintenance windows")
)

type Window struct {
	ID              uuid.UUID   `json:"id"`
	Name            string      `json:"name"`
	StartsAt        time.Time   `json:"starts_at"`
	DurationMinutes int         `json:"duration_minutes"`
	Repeat          string      `json:"repeat"`
	AllMonitors     bool        `json:"all_monitors"`
	MonitorIDs      []uuid.UUID `json:"monitor_ids"`
	Status          string      `json:"status"`
	// NextStart is when the window next begins; nil once a one-off window is over.
	NextStart *time.Time `json:"next_start"`
	CreatedAt time.Time  `json:"created_at"`
}

type Input struct {
	Name            string      `json:"name"`
	StartsAt        time.Time   `json:"starts_at"`
	DurationMinutes int         `json:"duration_minutes"`
	Repeat          string      `json:"repeat"`
	AllMonitors     bool        `json:"all_monitors"`
	MonitorIDs      []uuid.UUID `json:"monitor_ids"`
}

type Service struct {
	pool  *pgxpool.Pool
	plans *plans.Service
}

func NewService(pool *pgxpool.Pool, p *plans.Service) *Service { return &Service{pool: pool, plans: p} }

func period(repeat string) time.Duration {
	switch repeat {
	case RepeatDaily:
		return 24 * time.Hour
	case RepeatWeekly:
		return 7 * 24 * time.Hour
	}
	return 0
}

// nextStart is the next time the window begins after now (its first start while that is still ahead); nil if it never will again.
func nextStart(startsAt time.Time, repeat string, now time.Time) *time.Time {
	if now.Before(startsAt) {
		return &startsAt
	}
	p := period(repeat)
	if p == 0 {
		return nil
	}
	n := now.Sub(startsAt)/p + 1
	t := startsAt.Add(n * p)
	return &t
}

func (w *Window) finish(active bool, now time.Time) {
	w.NextStart = nextStart(w.StartsAt, w.Repeat, now)
	switch {
	case active:
		w.Status = StatusActive
	case w.NextStart != nil:
		w.Status = StatusUpcoming
	default:
		w.Status = StatusEnded
	}
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Window, error) {
	rows, err := s.pool.Query(ctx, `SELECT w.id, w.name, w.starts_at, w.duration_minutes, w.repeat, w.all_monitors, w.created_at,
			maintenance_window_active(w, now()),
			COALESCE(array_agg(wm.monitor_id ORDER BY wm.monitor_id) FILTER (WHERE wm.monitor_id IS NOT NULL), '{}')
		FROM maintenance_windows w LEFT JOIN maintenance_window_monitors wm ON wm.window_id = w.id
		WHERE w.organization_id = $1 GROUP BY w.id ORDER BY w.starts_at, w.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []Window{}
	for rows.Next() {
		var w Window
		var active bool
		if err := rows.Scan(&w.ID, &w.Name, &w.StartsAt, &w.DurationMinutes, &w.Repeat, &w.AllMonitors, &w.CreatedAt, &active, &w.MonitorIDs); err != nil {
			return nil, err
		}
		w.finish(active, now)
		out = append(out, w)
	}
	return out, rows.Err()
}

// Create validates and stores a window. Maintenance windows are a paid feature, so the plan is checked first.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, in Input) (Window, error) {
	if err := s.plans.CanUseMaintenance(ctx, s.pool, orgID); err != nil {
		return Window{}, err
	}
	in, err := validate(in, time.Now())
	if err != nil {
		return Window{}, err
	}
	var id uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM maintenance_windows WHERE organization_id = $1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxWindowsPerOrganization {
			return ErrTooMany
		}
		if !in.AllMonitors {
			var found int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL AND id = ANY($2)`, orgID, in.MonitorIDs).Scan(&found); err != nil {
				return err
			}
			if found != len(in.MonitorIDs) {
				return validation.New("monitor_ids", "contains a monitor that does not exist")
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, repeat, all_monitors)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, orgID, in.Name, in.StartsAt, in.DurationMinutes, in.Repeat, in.AllMonitors).Scan(&id); err != nil {
			return err
		}
		if in.AllMonitors {
			return nil
		}
		_, err := tx.Exec(ctx, `INSERT INTO maintenance_window_monitors (window_id, monitor_id) SELECT $1, unnest($2::uuid[])`, id, in.MonitorIDs)
		return err
	})
	if err != nil {
		return Window{}, err
	}
	list, err := s.List(ctx, orgID)
	if err != nil {
		return Window{}, err
	}
	for _, w := range list {
		if w.ID == id {
			return w, nil
		}
	}
	return Window{}, ErrNotFound
}

func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM maintenance_windows WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func validate(in Input, now time.Time) (Input, error) {
	bad := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 100 {
		bad["name"] = "must be 1 to 100 characters"
	}
	if in.Repeat == "" {
		in.Repeat = RepeatNone
	}
	switch in.Repeat {
	case RepeatNone, RepeatDaily, RepeatWeekly:
	default:
		bad["repeat"] = "must be none, daily or weekly"
	}
	if in.StartsAt.IsZero() {
		bad["starts_at"] = "is required"
	}
	maxMinutes := MaxDurationMinutes
	if p := period(in.Repeat); p > 0 {
		maxMinutes = int(p/time.Minute) - 1 // a repeating window must end before the next one starts
	}
	if in.DurationMinutes < 1 || in.DurationMinutes > maxMinutes {
		bad["duration_minutes"] = "must be between 1 and " + strconv.Itoa(maxMinutes)
	}
	_, badStart := bad["starts_at"]
	_, badDuration := bad["duration_minutes"]
	if !badStart && !badDuration && in.Repeat == RepeatNone &&
		!in.StartsAt.Add(time.Duration(in.DurationMinutes)*time.Minute).After(now) {
		bad["starts_at"] = "must not be a window that has already ended"
	}
	in.StartsAt = in.StartsAt.UTC().Truncate(time.Second)
	if in.AllMonitors {
		in.MonitorIDs = nil
	} else {
		seen := map[uuid.UUID]bool{}
		unique := in.MonitorIDs[:0:0]
		for _, id := range in.MonitorIDs {
			if !seen[id] {
				seen[id] = true
				unique = append(unique, id)
			}
		}
		in.MonitorIDs = unique
		if len(unique) == 0 {
			bad["monitor_ids"] = "choose at least one monitor, or apply the window to all monitors"
		} else if len(unique) > 500 {
			bad["monitor_ids"] = "too many monitors"
		}
	}
	if len(bad) > 0 {
		return in, &validation.Error{Fields: bad}
	}
	return in, nil
}
