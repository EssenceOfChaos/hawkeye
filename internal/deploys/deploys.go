// Package deploys lets a customer's CI pipeline tell HawkEye about a deploy (docs/plan-deploy-maintenance.md). Starting a deploy quiets alerts for
// the monitors it affects: a maintenance window of kind 'deploy' that begins now and ends by itself after its minutes. Finishing it ends the quiet
// period at once and checks those monitors right away, so the pipeline can fail (and roll back) when the site isn't healthy.
//
// The checks a pipeline asks for are not recorded with the monitor's results: a site still warming up would otherwise fail twice in a row and page
// someone, which is what a deploy window is meant to prevent. Once the window ends, the monitor's scheduled checks alert as usual.
package deploys

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/plans"
	"hawkeye/internal/runcheck"
	"hawkeye/internal/secrets"
	"hawkeye/internal/validation"
)

const (
	DefaultMinutes = 15
	MaxMinutes     = 120
	MaxNoteChars   = 200
	// PerHour is how many deploys an organization may start in an hour (extending an open one counts too).
	PerHour = 30
	// MaxChecked is how many monitors one check may cover: each is checked at once, and the whole check must fit in an API request.
	MaxChecked = 10
	// checkBudget is how long a check may take in all, checkTimeout how long one monitor's check may take, and retryAfter the pause before a
	// failing monitor is tried once more (a site that has just been deployed often needs a moment). The API's request limit is 30 s.
	checkBudget  = 22 * time.Second
	checkTimeout = 12 * time.Second
	retryAfter   = 4 * time.Second
)

var (
	ErrNotFound = errors.New("deploy not found")
	ErrTooMany  = errors.New("too many deploys started in the last hour")
)

// Deploy is a deploy window as the API shows it.
type Deploy struct {
	ID          uuid.UUID  `json:"id"`
	Tag         *string    `json:"tag"` // the tag it was started for; null for every monitor or a list of monitors
	AllMonitors bool       `json:"all_monitors"`
	Monitors    int        `json:"monitors"` // how many monitors it quiets (every monitor: how many there are now)
	Note        *string    `json:"note"`
	StartedAt   time.Time  `json:"started_at"`
	EndsAt      time.Time  `json:"ends_at"`  // when the quiet period ends (or ended)
	Active      bool       `json:"active"`   // alerts are quiet right now
	Verified    *bool      `json:"verified"` // the last check passed or failed; null if none ran
	ByKey       *string    `json:"started_by_key"`
	FinishedAt  *time.Time `json:"finished_at"` // when the pipeline finished it; null if it ended by itself or is still open
}

// Target names the monitors a deploy or check is for: a tag's name, a list of monitors, or (neither) every monitor.
type Target struct {
	Tag        string      `json:"tag" form:"tag"`
	MonitorIDs []uuid.UUID `json:"monitor_ids" form:"monitor_ids"`
}

// StartInput starts a deploy.
type StartInput struct {
	Target
	Minutes int    `json:"minutes" form:"minutes"` // 0: DefaultMinutes
	Note    string `json:"note" form:"note"`
}

// Check is one monitor's outcome in a check after a deploy.
type Check struct {
	MonitorID      uuid.UUID `json:"monitor_id"`
	Name           string    `json:"name"`
	Passed         bool      `json:"passed"`
	StatusCode     *int      `json:"status_code"`
	ResponseTimeMs *int      `json:"response_time_ms"`
	Error          *string   `json:"error"` // why it failed (the check's own summary)
	Attempts       int       `json:"attempts"`
}

type Service struct {
	pool    *pgxpool.Pool
	plans   *plans.Service
	checker *checks.Checker
	box     *secrets.Box
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, p *plans.Service, checker *checks.Checker, box *secrets.Box) *Service {
	return &Service{pool: pool, plans: p, checker: checker, box: box, now: time.Now}
}

// resolved is a Target turned into monitors.
type resolved struct {
	tag         *string
	all         bool
	monitorIDs  []uuid.UUID // the monitors named (empty with all)
	tagMonitors bool        // monitorIDs came from a tag
}

func (s *Service) resolve(ctx context.Context, q pgx.Tx, orgID uuid.UUID, t Target) (resolved, error) {
	tag := strings.TrimSpace(t.Tag)
	switch {
	case tag != "" && len(t.MonitorIDs) > 0:
		return resolved{}, validation.New("tag", "give a tag or monitor_ids, not both")
	case tag != "":
		var name string
		var ids []uuid.UUID
		err := q.QueryRow(ctx, `SELECT t.name, COALESCE(array_agg(m.id ORDER BY m.id) FILTER (WHERE m.id IS NOT NULL), '{}')
			FROM tags t LEFT JOIN monitor_tags mt ON mt.tag_id = t.id LEFT JOIN monitors m ON m.id = mt.monitor_id AND m.deleted_at IS NULL
			WHERE t.organization_id = $1 AND lower(t.name) = lower($2) GROUP BY t.id`, orgID, tag).Scan(&name, &ids)
		if errors.Is(err, pgx.ErrNoRows) {
			return resolved{}, validation.New("tag", "there is no tag named "+quote(tag))
		}
		if err != nil {
			return resolved{}, err
		}
		if len(ids) == 0 {
			return resolved{}, validation.New("tag", "no monitors have the tag "+quote(name))
		}
		return resolved{tag: &name, monitorIDs: ids, tagMonitors: true}, nil
	case len(t.MonitorIDs) > 0:
		ids := slices.Clone(t.MonitorIDs)
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		ids = slices.Compact(ids)
		if len(ids) > 500 {
			return resolved{}, validation.New("monitor_ids", "too many monitors")
		}
		var found int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL AND id = ANY($2)`, orgID, ids).Scan(&found); err != nil {
			return resolved{}, err
		}
		if found != len(ids) {
			return resolved{}, validation.New("monitor_ids", "contains a monitor that does not exist")
		}
		return resolved{monitorIDs: ids}, nil
	}
	return resolved{all: true}, nil
}

func quote(s string) string { return "“" + s + "”" }

// Start opens a deploy window, or extends the open one for the same monitors (so a retried pipeline step doesn't stack windows). Maintenance is a
// paid feature, so the plan is checked first. extended reports which happened.
func (s *Service) Start(ctx context.Context, orgID uuid.UUID, tokenID *uuid.UUID, in StartInput) (d Deploy, extended bool, err error) {
	if err := s.plans.CanUseMaintenance(ctx, s.pool, orgID); err != nil {
		return Deploy{}, false, err
	}
	bad := map[string]string{}
	if in.Minutes == 0 {
		in.Minutes = DefaultMinutes
	}
	if in.Minutes < 1 || in.Minutes > MaxMinutes {
		bad["minutes"] = "must be between 1 and 120"
	}
	note := strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(note) > MaxNoteChars || strings.ContainsAny(note, "\r\n") {
		bad["note"] = "must be at most 200 characters on one line"
	}
	if len(bad) > 0 {
		return Deploy{}, false, &validation.Error{Fields: bad}
	}
	var id uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// One deploy at a time per organization is decided here, so two pipeline steps racing don't both open a window.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		r, err := s.resolve(ctx, tx, orgID, in.Target)
		if err != nil {
			return err
		}
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM maintenance_windows WHERE organization_id = $1 AND kind = 'deploy'
			AND created_at > now() - interval '1 hour'`, orgID).Scan(&recent); err != nil {
			return err
		}
		if recent >= PerHour {
			return ErrTooMany
		}
		now := s.now().UTC().Truncate(time.Second)
		if open, err := s.openFor(ctx, tx, orgID, r, now); err != nil {
			return err
		} else if open != uuid.Nil {
			// Extend it to end minutes from now; a newer note replaces the old one.
			id, extended = open, true
			_, err := tx.Exec(ctx, `UPDATE maintenance_windows SET
					duration_minutes = GREATEST(duration_minutes, CEIL(EXTRACT(EPOCH FROM ($2::timestamptz + make_interval(mins => $3) - starts_at)) / 60)::int),
					note = COALESCE(NULLIF($4, ''), note), started_by_token = COALESCE($5, started_by_token)
				WHERE id = $1`, id, now, in.Minutes, note, tokenID)
			return err
		}
		name := "Deploy"
		if r.tag != nil {
			name = "Deploy: " + *r.tag
		}
		if err := tx.QueryRow(ctx, `INSERT INTO maintenance_windows (organization_id, kind, name, starts_at, duration_minutes, all_monitors, target, note,
				started_by_token) VALUES ($1, 'deploy', $2, $3, $4, $5, $6, NULLIF($7, ''), $8) RETURNING id`,
			orgID, truncateName(name), now, in.Minutes, r.all, r.tag, note, tokenID).Scan(&id); err != nil {
			return err
		}
		if r.all {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO maintenance_window_monitors (window_id, monitor_id) SELECT $1, unnest($2::uuid[])`, id, r.monitorIDs)
		return err
	})
	if err != nil {
		return Deploy{}, false, err
	}
	d, err = s.get(ctx, orgID, id)
	return d, extended, err
}

func truncateName(s string) string {
	if utf8.RuneCountInString(s) <= 100 {
		return s
	}
	return string([]rune(s)[:100])
}

// openFor finds an open deploy for exactly the same monitors (the same tag, the same list, or every monitor), or uuid.Nil.
func (s *Service) openFor(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, r resolved, now time.Time) (uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT w.id, w.all_monitors, w.target,
			COALESCE(array_agg(wm.monitor_id ORDER BY wm.monitor_id) FILTER (WHERE wm.monitor_id IS NOT NULL), '{}')
		FROM maintenance_windows w LEFT JOIN maintenance_window_monitors wm ON wm.window_id = w.id
		WHERE w.organization_id = $1 AND w.kind = 'deploy' AND maintenance_window_active(w, $2)
		GROUP BY w.id ORDER BY w.starts_at DESC`, orgID, now)
	if err != nil {
		return uuid.Nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var all bool
		var tag *string
		var ids []uuid.UUID
		if err := rows.Scan(&id, &all, &tag, &ids); err != nil {
			return uuid.Nil, err
		}
		switch {
		case r.all && all,
			r.tag != nil && tag != nil && strings.EqualFold(*r.tag, *tag),
			r.tag == nil && tag == nil && !r.all && !all && slices.Equal(sorted(ids), sorted(r.monitorIDs)):
			return id, nil
		}
	}
	return uuid.Nil, rows.Err()
}

func sorted(ids []uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// Finish ends a deploy's quiet period now (if it is still open) and, with verify, checks its monitors. ref is a deploy's id, or "latest": the
// most recent deploy started with this key (any key or none, for a person), within the last day. Finishing again is allowed: the pipeline may
// retry until the site is healthy.
func (s *Service) Finish(ctx context.Context, orgID uuid.UUID, tokenID *uuid.UUID, ref string, verify bool) (Deploy, []Check, bool, error) {
	id, err := s.find(ctx, orgID, tokenID, ref)
	if err != nil {
		return Deploy{}, nil, false, err
	}
	now := s.now()
	if _, err := s.pool.Exec(ctx, `UPDATE maintenance_windows w SET ended_at = $3
		WHERE id = $1 AND organization_id = $2 AND ended_at IS NULL AND maintenance_window_active(w, $3)`, id, orgID, now); err != nil {
		return Deploy{}, nil, false, err
	}
	var results []Check
	passed := true
	if verify {
		var all bool
		var ids []uuid.UUID
		if err := s.pool.QueryRow(ctx, `SELECT w.all_monitors, COALESCE(array_agg(wm.monitor_id) FILTER (WHERE wm.monitor_id IS NOT NULL), '{}')
			FROM maintenance_windows w LEFT JOIN maintenance_window_monitors wm ON wm.window_id = w.id WHERE w.id = $1 GROUP BY w.id`, id).Scan(&all, &ids); err != nil {
			return Deploy{}, nil, false, err
		}
		results, passed, err = s.check(ctx, orgID, resolved{all: all, monitorIDs: ids})
		if err != nil {
			return Deploy{}, nil, false, err
		}
		if _, err := s.pool.Exec(ctx, `UPDATE maintenance_windows SET verified = $2 WHERE id = $1`, id, passed); err != nil {
			return Deploy{}, nil, false, err
		}
	}
	d, err := s.get(ctx, orgID, id)
	return d, results, passed, err
}

// CheckNow checks the named monitors without any deploy window: a pipeline check on its own, on every plan.
func (s *Service) CheckNow(ctx context.Context, orgID uuid.UUID, t Target) ([]Check, bool, error) {
	var r resolved
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		r, err = s.resolve(ctx, tx, orgID, t)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return s.check(ctx, orgID, r)
}

func (s *Service) find(ctx context.Context, orgID uuid.UUID, tokenID *uuid.UUID, ref string) (uuid.UUID, error) {
	var id uuid.UUID
	var err error
	if ref == "latest" {
		err = s.pool.QueryRow(ctx, `SELECT id FROM maintenance_windows WHERE organization_id = $1 AND kind = 'deploy'
			AND ($2::uuid IS NULL OR started_by_token = $2) AND created_at > now() - interval '1 day'
			ORDER BY created_at DESC LIMIT 1`, orgID, tokenID).Scan(&id)
	} else {
		parsed, perr := uuid.Parse(ref)
		if perr != nil {
			return uuid.Nil, ErrNotFound
		}
		err = s.pool.QueryRow(ctx, `SELECT id FROM maintenance_windows WHERE id = $1 AND organization_id = $2 AND kind = 'deploy'`, parsed, orgID).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// check runs every named monitor's check at once (heartbeats and paused monitors are skipped), and tries a failing one once more after a short
// pause when there is time. The whole check fits in an API request.
func (s *Service) check(ctx context.Context, orgID uuid.UUID, r resolved) ([]Check, bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL AND enabled
			AND kind <> 'heartbeat' AND ($2 OR id = ANY($3)) ORDER BY name, id`, orgID, r.all, r.monitorIDs)
	if err != nil {
		return nil, false, err
	}
	var out []Check
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.MonitorID, &c.Name); err != nil {
			rows.Close()
			return nil, false, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > MaxChecked {
		return nil, false, validation.New("monitors", "a check covers at most 10 monitors: name a tag with fewer, or finish with verify=false")
	}
	ctx, cancel := context.WithDeadline(ctx, s.now().Add(checkBudget))
	defer cancel()
	run := func(todo []int) {
		var wg sync.WaitGroup
		for _, i := range todo {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.checkOne(ctx, &out[i])
			}()
		}
		wg.Wait()
	}
	all := make([]int, len(out))
	for i := range out {
		all[i] = i
	}
	run(all)
	var failing []int
	for i, c := range out {
		if !c.Passed {
			failing = append(failing, i)
		}
	}
	if dl, _ := ctx.Deadline(); len(failing) > 0 && time.Until(dl) > retryAfter+2*time.Second {
		select {
		case <-ctx.Done():
		case <-time.After(retryAfter):
			run(failing)
		}
	}
	passed := true
	for _, c := range out {
		passed = passed && c.Passed
	}
	if out == nil {
		out = []Check{}
	}
	return out, passed, nil
}

func (s *Service) checkOne(ctx context.Context, c *Check) {
	c.Attempts++
	res, err := runcheck.Run(ctx, s.pool, s.checker, s.box, c.MonitorID, checkTimeout)
	if err != nil {
		msg := "Couldn’t be checked"
		switch {
		case errors.Is(err, context.DeadlineExceeded), ctx.Err() != nil:
			msg = "Didn’t finish in time: a check after a deploy allows about 20 seconds in all"
		case errors.Is(err, runcheck.ErrSetup):
			msg = "Its stored credentials couldn’t be opened"
		}
		c.Passed, c.Error = false, &msg
		return
	}
	c.Passed, c.StatusCode, c.ResponseTimeMs, c.Error = res.Success, res.StatusCode, res.ResponseTimeMs, nil
	if !res.Success {
		msg := res.ErrorSummary
		c.Error = &msg
	}
}

// List returns the organization's most recent deploys, newest first.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, limit int) ([]Deploy, error) {
	rows, err := s.pool.Query(ctx, selectDeploy+` WHERE w.organization_id = $1 AND w.kind = 'deploy' GROUP BY w.id, k.name
		ORDER BY w.created_at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deploy{}
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) get(ctx context.Context, orgID, id uuid.UUID) (Deploy, error) {
	d, err := scanDeploy(s.pool.QueryRow(ctx, selectDeploy+` WHERE w.id = $1 AND w.organization_id = $2 GROUP BY w.id, k.name`, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deploy{}, ErrNotFound
	}
	return d, err
}

const selectDeploy = `SELECT w.id, w.target, w.all_monitors,
		CASE WHEN w.all_monitors THEN (SELECT count(*) FROM monitors m WHERE m.organization_id = w.organization_id AND m.deleted_at IS NULL)
			ELSE count(wm.monitor_id) END::int,
		w.note, w.starts_at, COALESCE(w.ended_at, w.starts_at + make_interval(mins => w.duration_minutes)), maintenance_window_active(w, now()),
		w.verified, k.name, w.ended_at
	FROM maintenance_windows w LEFT JOIN maintenance_window_monitors wm ON wm.window_id = w.id LEFT JOIN api_tokens k ON k.id = w.started_by_token`

func scanDeploy(row pgx.Row) (Deploy, error) {
	var d Deploy
	err := row.Scan(&d.ID, &d.Tag, &d.AllMonitors, &d.Monitors, &d.Note, &d.StartedAt, &d.EndsAt, &d.Active, &d.Verified, &d.ByKey, &d.FinishedAt)
	return d, err
}
