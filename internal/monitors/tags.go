package monitors

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hawkeye/internal/validation"
)

const (
	MaxTagsPerOrganization = 100
	MaxTagsPerMonitor      = 10
	MaxTagNameChars        = 40
	// MaxBulkMonitors is how many monitors one bulk action may change: the largest plan's monitor limit.
	MaxBulkMonitors = 200
)

var (
	ErrTagNotFound  = errors.New("tag not found")
	ErrTagDuplicate = errors.New("a tag with this name already exists")
	ErrTooManyTags  = errors.New("too many tags")
)

// Tag groups monitors, for example by client or project.
type Tag struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Color *string   `json:"color"` // #rrggbb, or null for the default look
}

// TagSummary is a tag with the number of (not deleted) monitors that carry it.
type TagSummary struct {
	Tag
	MonitorCount int `json:"monitor_count"`
}

type TagInput struct {
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

type TagUpdate struct {
	Name  *string          `json:"name"`
	Color Nullable[string] `json:"color"`
}

// tagsCol is a monitor's tags as a JSON array, sorted by name, for the monitor columns.
const tagsCol = `COALESCE((SELECT json_agg(json_build_object('id', t.id, 'name', t.name, 'color', t.color) ORDER BY lower(t.name), t.id)
		FROM monitor_tags mt JOIN tags t ON t.id = mt.tag_id WHERE mt.monitor_id = monitors.id), '[]')`

var colorRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func cleanTagName(name string) (string, string) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxTagNameChars {
		return "", fmt.Sprintf("must be between 1 and %d characters", MaxTagNameChars)
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", "must not contain control characters"
	}
	return name, ""
}

func cleanColor(c *string) (*string, string) {
	if c == nil || strings.TrimSpace(*c) == "" {
		return nil, ""
	}
	v := strings.ToLower(strings.TrimSpace(*c))
	if !colorRE.MatchString(v) {
		return nil, "must be a color like #1a73e8, or null"
	}
	return &v, ""
}

func scanTagSummary(row pgx.Row) (TagSummary, error) {
	var t TagSummary
	err := row.Scan(&t.ID, &t.Name, &t.Color, &t.MonitorCount)
	return t, err
}

const tagCols = `id, name, color, (SELECT count(*) FROM monitor_tags mt JOIN monitors m ON m.id = mt.monitor_id
	WHERE mt.tag_id = tags.id AND m.deleted_at IS NULL)`

// ListTags returns the organization's tags sorted by name.
func (s *Service) ListTags(ctx context.Context, orgID uuid.UUID) ([]TagSummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+tagCols+` FROM tags WHERE organization_id = $1 ORDER BY lower(name), id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagSummary{}
	for rows.Next() {
		t, err := scanTagSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) CreateTag(ctx context.Context, orgID uuid.UUID, in TagInput) (TagSummary, error) {
	name, bad := cleanTagName(in.Name)
	color, badColor := cleanColor(in.Color)
	if err := tagFieldErrors(bad, badColor); err != nil {
		return TagSummary{}, err
	}
	var out TagSummary
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialize per organization so the count check cannot be raced.
		if err := lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM tags WHERE organization_id = $1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxTagsPerOrganization {
			return ErrTooManyTags
		}
		var err error
		out, err = scanTagSummary(tx.QueryRow(ctx, `INSERT INTO tags (organization_id, name, color) VALUES ($1, $2, $3) RETURNING `+tagCols,
			orgID, name, color))
		return err
	})
	return out, tagWriteError(err)
}

func (s *Service) UpdateTag(ctx context.Context, orgID, id uuid.UUID, in TagUpdate) (TagSummary, error) {
	var name *string
	var bad, badColor string
	if in.Name != nil {
		var n string
		n, bad = cleanTagName(*in.Name)
		name = &n
	}
	var color *string
	if in.Color.Set {
		color, badColor = cleanColor(in.Color.Value)
	}
	if err := tagFieldErrors(bad, badColor); err != nil {
		return TagSummary{}, err
	}
	out, err := scanTagSummary(s.pool.QueryRow(ctx, `UPDATE tags SET name = COALESCE($3, name), color = CASE WHEN $4 THEN $5 ELSE color END
		WHERE id = $1 AND organization_id = $2 RETURNING `+tagCols, id, orgID, name, in.Color.Set, color))
	return out, tagWriteError(err)
}

// DeleteTag removes a tag; its monitors stay, without it.
func (s *Service) DeleteTag(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM tags WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTagNotFound
	}
	return nil
}

func tagFieldErrors(name, color string) error {
	bad := map[string]string{}
	if name != "" {
		bad["name"] = name
	}
	if color != "" {
		bad["color"] = color
	}
	if len(bad) > 0 {
		return &validation.Error{Fields: bad}
	}
	return nil
}

func tagWriteError(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrTagNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return ErrTagDuplicate
	}
	return err
}

// resolveTags removes repeats from ids and checks every tag belongs to the organization, so a monitor can never be linked to another
// organization's tag.
func resolveTags(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	out := []uuid.UUID{}
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM tags WHERE organization_id = $1 AND id = ANY($2)`, orgID, out).Scan(&n); err != nil {
		return nil, err
	}
	if n != len(out) {
		return nil, validation.New("tag_ids", "contains a tag that does not exist")
	}
	return out, nil
}

// setTags makes ids (already resolved) exactly the monitor's tags.
func setTags(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) > MaxTagsPerMonitor {
		return validation.New("tag_ids", fmt.Sprintf("at most %d tags per monitor", MaxTagsPerMonitor))
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitor_tags WHERE monitor_id = $1 AND NOT (tag_id = ANY($2))`, monitorID, ids); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO monitor_tags (monitor_id, tag_id) SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`, monitorID, ids)
	return err
}

// Bulk actions.
const (
	BulkPause       = "pause"
	BulkResume      = "resume"
	BulkDelete      = "delete"
	BulkSetInterval = "set_interval"
	BulkAddTags     = "add_tags"
	BulkRemoveTags  = "remove_tags"
	// BulkSetAlertTiming sets the alert delay, the quiet hours, or both; whichever is left out stays as it is on each monitor.
	BulkSetAlertTiming = "set_alert_timing"
)

// BulkInput applies one action to several monitors at once. CheckIntervalSeconds is for set_interval, TagIDs for add_tags and remove_tags, and
// AlertDelayMinutes and QuietHours (null turns quiet hours off) for set_alert_timing.
type BulkInput struct {
	MonitorIDs           []uuid.UUID          `json:"monitor_ids"`
	Action               string               `json:"action"`
	CheckIntervalSeconds *int                 `json:"check_interval_seconds"`
	TagIDs               []uuid.UUID          `json:"tag_ids"`
	AlertDelayMinutes    *int                 `json:"alert_delay_minutes"`
	QuietHours           Nullable[QuietHours] `json:"quiet_hours"`
}

// Bulk applies an action to the listed monitors, all or nothing: if any monitor is missing (or in another organization) nothing changes and
// the result is ErrNotFound; if the action is not allowed for any of them nothing changes either. It returns how many monitors it changed.
func (s *Service) Bulk(ctx context.Context, orgID uuid.UUID, in BulkInput) (int, error) {
	bad := map[string]string{}
	var quietStart, quietEnd *int // set_alert_timing: the stored form of the quiet hours (nil with QuietHours.Set: turn them off)
	var quietTZ *string
	ids := []uuid.UUID{}
	for _, id := range in.MonitorIDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > MaxBulkMonitors {
		bad["monitor_ids"] = fmt.Sprintf("must list between 1 and %d monitors", MaxBulkMonitors)
	}
	switch in.Action {
	case BulkPause, BulkResume, BulkDelete:
	case BulkSetInterval:
		if in.CheckIntervalSeconds == nil {
			bad["check_interval_seconds"] = "is required"
		} else if v := *in.CheckIntervalSeconds; v < MinIntervalSeconds || v > MaxIntervalSeconds {
			bad["check_interval_seconds"] = fmt.Sprintf("must be between %d and %d", MinIntervalSeconds, MaxIntervalSeconds)
		}
	case BulkAddTags, BulkRemoveTags:
		if len(in.TagIDs) == 0 {
			bad["tag_ids"] = "must list at least one tag"
		}
	case BulkSetAlertTiming:
		switch {
		case in.AlertDelayMinutes == nil && !in.QuietHours.Set:
			bad["alert_delay_minutes"] = "give alert_delay_minutes, quiet_hours, or both"
		case in.AlertDelayMinutes != nil && (*in.AlertDelayMinutes < 0 || *in.AlertDelayMinutes > MaxAlertDelayMinutes):
			bad["alert_delay_minutes"] = fmt.Sprintf("must be between 0 and %d minutes", MaxAlertDelayMinutes)
		}
		if in.QuietHours.Set {
			quietStart, quietEnd, quietTZ = cleanQuiet(in.QuietHours.Value, bad)
		}
	default:
		bad["action"] = "must be pause, resume, delete, set_interval, add_tags, remove_tags or set_alert_timing"
	}
	if len(bad) > 0 {
		return 0, &validation.Error{Fields: bad}
	}

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Monitor rows first, then (for deletes) the organization row: the same order as single edits and deletes, so they cannot deadlock.
		rows, err := tx.Query(ctx, `SELECT method FROM monitors WHERE organization_id = $1 AND id = ANY($2) AND deleted_at IS NULL FOR UPDATE`, orgID, ids)
		if err != nil {
			return err
		}
		writes := 0
		found := 0
		for rows.Next() {
			var method string
			if err := rows.Scan(&method); err != nil {
				rows.Close()
				return err
			}
			found++
			if IsWriteMethod(method) {
				writes++
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if found != len(ids) {
			return ErrNotFound
		}

		switch in.Action {
		case BulkPause:
			_, err = tx.Exec(ctx, `UPDATE monitors SET enabled = false WHERE id = ANY($1)`, ids)
		case BulkResume:
			// Resuming makes a monitor due immediately instead of waiting out a stale schedule, and gives a heartbeat a full period to ping.
			var minInterval int
			if minInterval, err = s.plans.GetMinimumCheckInterval(ctx, tx, orgID); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE monitors SET enabled = true,
				next_check_at = CASE WHEN NOT enabled AND kind <> 'heartbeat' THEN now() ELSE next_check_at END, `+resumeDeadline+`
				WHERE id = ANY($1) AND organization_id = $2`, ids, orgID, true, minInterval)
		case BulkDelete:
			if _, err = deleteTx(ctx, tx, orgID, ids); err != nil {
				return err
			}
			err = s.lockedDomainSync(ctx, tx, orgID)
		case BulkSetInterval:
			v := *in.CheckIntervalSeconds
			if writes > 0 && v < MinWriteIntervalSeconds {
				return validation.New("check_interval_seconds", fmt.Sprintf(
					"must be at least %d minutes: %d of the selected monitors use POST, PUT, PATCH or DELETE, which repeat the request on every check",
					MinWriteIntervalSeconds/60, writes))
			}
			if err := s.plans.CheckInterval(ctx, tx, orgID, v); err != nil {
				return err
			}
			// A heartbeat's period is when its job runs, which a check interval says nothing about: heartbeats are left as they are.
			_, err = tx.Exec(ctx, `UPDATE monitors SET check_interval_seconds = $2 WHERE id = ANY($1) AND kind <> 'heartbeat'`, ids, v)
		case BulkAddTags:
			var tagIDs []uuid.UUID
			if tagIDs, err = resolveTags(ctx, tx, orgID, in.TagIDs); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO monitor_tags (monitor_id, tag_id)
				SELECT m.id, t.id FROM unnest($1::uuid[]) AS m(id) CROSS JOIN unnest($2::uuid[]) AS t(id) ON CONFLICT DO NOTHING`, ids, tagIDs); err != nil {
				return err
			}
			var over int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM monitor_tags WHERE monitor_id = ANY($1)
				GROUP BY monitor_id HAVING count(*) > $2) o`, ids, MaxTagsPerMonitor).Scan(&over); err != nil {
				return err
			}
			if over > 0 {
				return validation.New("tag_ids", fmt.Sprintf("%d of the selected monitors would have more than %d tags", over, MaxTagsPerMonitor))
			}
		case BulkRemoveTags:
			var tagIDs []uuid.UUID
			if tagIDs, err = resolveTags(ctx, tx, orgID, in.TagIDs); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `DELETE FROM monitor_tags WHERE monitor_id = ANY($1) AND tag_id = ANY($2)`, ids, tagIDs)
		case BulkSetAlertTiming:
			// Like a single edit, this affects alerts raised from now on; alerts already held keep their schedule.
			_, err = tx.Exec(ctx, `UPDATE monitors SET alert_delay_minutes = COALESCE($2, alert_delay_minutes),
				quiet_start = CASE WHEN $3 THEN $4 ELSE quiet_start END, quiet_end = CASE WHEN $3 THEN $5 ELSE quiet_end END,
				quiet_timezone = CASE WHEN $3 THEN $6 ELSE quiet_timezone END
				WHERE id = ANY($1)`, ids, in.AlertDelayMinutes, in.QuietHours.Set, quietStart, quietEnd, quietTZ)
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}
