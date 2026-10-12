// Package monitors manages monitor definitions. Every query is scoped by organization ID, which
// callers must have authorised; a monitor in another organization is indistinguishable from a missing one.
package monitors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/validation"
)

var ErrNotFound = errors.New("monitor not found")

const (
	DefaultTimeoutSeconds = 10
	MaxIntervalSeconds    = 86400
	MinIntervalSeconds    = 60
)

type Monitor struct {
	ID                   uuid.UUID `json:"id"`
	OrganizationID       uuid.UUID `json:"organization_id"`
	Name                 string    `json:"name"`
	URL                  string    `json:"url"`
	Method               string    `json:"method"`
	ExpectedStatusCode   *int      `json:"expected_status_code"`
	CheckIntervalSeconds int       `json:"check_interval_seconds"`
	// EffectiveCheckIntervalSeconds is what the scheduler uses: the stored value raised to the
	// plan minimum, so a downgrade takes effect without rewriting data.
	EffectiveCheckIntervalSeconds int        `json:"effective_check_interval_seconds"`
	TimeoutSeconds                int        `json:"timeout_seconds"`
	Enabled                       bool       `json:"enabled"`
	CurrentStatus                 string     `json:"current_status"`
	LastCheckedAt                 *time.Time `json:"last_checked_at"`
	LastSuccessAt                 *time.Time `json:"last_success_at"`
	ConsecutiveFailures           int        `json:"consecutive_failures"`
	ConsecutiveSuccesses          int        `json:"consecutive_successes"`
	CreatedAt                     time.Time  `json:"created_at"`
	UpdatedAt                     time.Time  `json:"updated_at"`
}

// Nullable distinguishes "field absent" from "field set to null" in PATCH bodies.
type Nullable[T any] struct {
	Set   bool
	Value *T
}

func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		n.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// Only client-editable fields exist here; state fields (status, counters, timestamps) cannot be set.
type CreateInput struct {
	Name                 string  `json:"name"`
	URL                  string  `json:"url"`
	Method               *string `json:"method"`
	ExpectedStatusCode   *int    `json:"expected_status_code"`
	CheckIntervalSeconds *int    `json:"check_interval_seconds"`
	TimeoutSeconds       *int    `json:"timeout_seconds"`
}

type UpdateInput struct {
	Name                 *string       `json:"name"`
	URL                  *string       `json:"url"`
	Method               *string       `json:"method"`
	ExpectedStatusCode   Nullable[int] `json:"expected_status_code"`
	CheckIntervalSeconds *int          `json:"check_interval_seconds"`
	TimeoutSeconds       *int          `json:"timeout_seconds"`
}

type ListFilter struct {
	Status  string // unknown|up|down, or empty
	Enabled *bool
	Query   string
	Limit   int
	Offset  int
}

type Service struct {
	pool  *pgxpool.Pool
	plans *plans.Service
}

func NewService(pool *pgxpool.Pool, p *plans.Service) *Service { return &Service{pool: pool, plans: p} }

const cols = `id, organization_id, name, url, method, expected_status_code, check_interval_seconds, timeout_seconds,
	enabled, current_status, last_checked_at, last_success_at, consecutive_failures, consecutive_successes, created_at, updated_at`

func scan(row pgx.Row, minInterval int) (Monitor, error) {
	var m Monitor
	err := row.Scan(&m.ID, &m.OrganizationID, &m.Name, &m.URL, &m.Method, &m.ExpectedStatusCode, &m.CheckIntervalSeconds,
		&m.TimeoutSeconds, &m.Enabled, &m.CurrentStatus, &m.LastCheckedAt, &m.LastSuccessAt, &m.ConsecutiveFailures,
		&m.ConsecutiveSuccesses, &m.CreatedAt, &m.UpdatedAt)
	m.EffectiveCheckIntervalSeconds = max(m.CheckIntervalSeconds, minInterval)
	return m, err
}

// fields are the validated, normalised editable values of a monitor.
type fields struct {
	name, url, method string
	expected          *int
	interval, timeout int
}

func (f *fields) validate() error {
	bad := map[string]string{}
	f.name = strings.TrimSpace(f.name)
	if n := utf8.RuneCountInString(f.name); n < 1 || n > 100 {
		bad["name"] = "must be between 1 and 100 characters"
	}
	if u, reason := NormalizeURL(f.url); reason != "" {
		bad["url"] = reason
	} else {
		f.url = u
	}
	f.method = strings.ToUpper(strings.TrimSpace(f.method))
	if f.method != "GET" && f.method != "HEAD" {
		bad["method"] = "must be GET or HEAD"
	}
	if f.expected != nil && (*f.expected < 100 || *f.expected > 599) {
		bad["expected_status_code"] = "must be between 100 and 599, or null"
	}
	if f.interval < MinIntervalSeconds || f.interval > MaxIntervalSeconds {
		bad["check_interval_seconds"] = fmt.Sprintf("must be between %d and %d", MinIntervalSeconds, MaxIntervalSeconds)
	}
	if f.timeout < 1 || f.timeout > 30 {
		bad["timeout_seconds"] = "must be between 1 and 30"
	}
	if len(bad) > 0 {
		return &validation.Error{Fields: bad}
	}
	return nil
}

// lockOrg serialises monitor creation per organization so concurrent requests cannot both
// pass the limit check.
func lockOrg(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Service) Create(ctx context.Context, orgID uuid.UUID, in CreateInput) (Monitor, error) {
	var out Monitor
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		minInterval, err := s.plans.GetMinimumCheckInterval(ctx, tx, orgID)
		if err != nil {
			return err
		}
		f := fields{name: in.Name, url: in.URL, method: "GET", expected: in.ExpectedStatusCode,
			interval: minInterval, timeout: DefaultTimeoutSeconds}
		if in.Method != nil {
			f.method = *in.Method
		}
		if in.CheckIntervalSeconds != nil {
			f.interval = *in.CheckIntervalSeconds
		}
		if in.TimeoutSeconds != nil {
			f.timeout = *in.TimeoutSeconds
		}
		if err := f.validate(); err != nil {
			return err
		}
		if err := s.plans.CanCreateMonitor(ctx, tx, orgID); err != nil {
			return err
		}
		if in.CheckIntervalSeconds != nil {
			if err := s.plans.CheckInterval(ctx, tx, orgID, f.interval); err != nil {
				return err
			}
		}
		out, err = scan(tx.QueryRow(ctx, `INSERT INTO monitors
			(organization_id, name, url, method, expected_status_code, check_interval_seconds, timeout_seconds)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+cols,
			orgID, f.name, f.url, f.method, f.expected, f.interval, f.timeout), minInterval)
		return err
	})
	return out, err
}

func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (Monitor, error) {
	min, err := s.plans.GetMinimumCheckInterval(ctx, s.pool, orgID)
	if err != nil {
		return Monitor{}, err
	}
	m, err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM monitors
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, id, orgID), min)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	return m, err
}

// List returns one page of monitors and the total matching count.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, f ListFilter) ([]Monitor, int, error) {
	min, err := s.plans.GetMinimumCheckInterval(ctx, s.pool, orgID)
	if err != nil {
		return nil, 0, err
	}
	where := `organization_id = $1 AND deleted_at IS NULL`
	args := []any{orgID}
	add := func(cond string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.Status != "" {
		add("current_status = $%d", f.Status)
	}
	if f.Enabled != nil {
		add("enabled = $%d", *f.Enabled)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		add(`(name ILIKE $%[1]d OR url ILIKE $%[1]d)`, "%"+esc+"%")
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM monitors WHERE %s
		ORDER BY created_at DESC, id LIMIT $%d OFFSET $%d`, cols, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scan(rows, min)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (s *Service) Update(ctx context.Context, orgID, id uuid.UUID, in UpdateInput) (Monitor, error) {
	var out Monitor
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		minInterval, err := s.plans.GetMinimumCheckInterval(ctx, tx, orgID)
		if err != nil {
			return err
		}
		cur, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM monitors
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL FOR UPDATE`, id, orgID), minInterval)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		f := fields{name: cur.Name, url: cur.URL, method: cur.Method, expected: cur.ExpectedStatusCode,
			interval: cur.CheckIntervalSeconds, timeout: cur.TimeoutSeconds}
		if in.Name != nil {
			f.name = *in.Name
		}
		if in.URL != nil {
			f.url = *in.URL
		}
		if in.Method != nil {
			f.method = *in.Method
		}
		if in.ExpectedStatusCode.Set {
			f.expected = in.ExpectedStatusCode.Value
		}
		if in.CheckIntervalSeconds != nil {
			f.interval = *in.CheckIntervalSeconds
		}
		if in.TimeoutSeconds != nil {
			f.timeout = *in.TimeoutSeconds
		}
		if err := f.validate(); err != nil {
			return err
		}
		// Only a requested interval change is checked: after a downgrade, unrelated edits still work.
		if in.CheckIntervalSeconds != nil {
			if err := s.plans.CheckInterval(ctx, tx, orgID, f.interval); err != nil {
				return err
			}
		}
		// A changed target invalidates the previous health state.
		reset := f.url != cur.URL || f.method != cur.Method
		out, err = scan(tx.QueryRow(ctx, `UPDATE monitors SET name=$3, url=$4, method=$5, expected_status_code=$6,
			check_interval_seconds=$7, timeout_seconds=$8,
			next_check_at = CASE WHEN $9 THEN now() ELSE next_check_at END,
			current_status = CASE WHEN $9 THEN 'unknown' ELSE current_status END,
			consecutive_failures = CASE WHEN $9 THEN 0 ELSE consecutive_failures END,
			consecutive_successes = CASE WHEN $9 THEN 0 ELSE consecutive_successes END
			WHERE id = $1 AND organization_id = $2 RETURNING `+cols,
			id, orgID, f.name, f.url, f.method, f.expected, f.interval, f.timeout, reset), minInterval)
		return err
	})
	return out, err
}

// SetEnabled pauses or resumes a monitor.
func (s *Service) SetEnabled(ctx context.Context, orgID, id uuid.UUID, enabled bool) (Monitor, error) {
	min, err := s.plans.GetMinimumCheckInterval(ctx, s.pool, orgID)
	if err != nil {
		return Monitor{}, err
	}
	// Resuming makes the monitor due immediately instead of waiting out a stale schedule.
	m, err := scan(s.pool.QueryRow(ctx, `UPDATE monitors SET enabled = $3,
		next_check_at = CASE WHEN $3 AND NOT enabled THEN now() ELSE next_check_at END
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL RETURNING `+cols, id, orgID, enabled), min)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	return m, err
}

// Delete soft-deletes a monitor: it stops counting toward limits and is no longer checked.
// An open incident is closed without notifications: nobody needs a recovery email for a monitor they removed.
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE monitors SET deleted_at = now(), enabled = false
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE incidents SET status = 'resolved', resolved_at = GREATEST(started_at, now())
			WHERE monitor_id = $1 AND status = 'open'`, id)
		return err
	})
}
