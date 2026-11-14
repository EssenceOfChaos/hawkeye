package incidents

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Filter selects incidents of one organization.
type Filter struct {
	MonitorID *uuid.UUID
	Status    string     // open | resolved | ""
	From, To  *time.Time // bounds on started_at
	Limit     int
	Offset    int
	id        *uuid.UUID // Get only
}

// Get returns one incident of the organization, or ErrIncidentNotFound (also for another organization's incident).
func Get(ctx context.Context, pool *pgxpool.Pool, orgID, incidentID uuid.UUID) (Incident, error) {
	list, _, err := List(ctx, pool, orgID, Filter{id: &incidentID, Limit: 1})
	if err != nil {
		return Incident{}, err
	}
	if len(list) == 0 {
		return Incident{}, ErrIncidentNotFound
	}
	return list[0], nil
}

// List returns one page of incidents (newest first) and the total count. Tenant scoping is by organization
// through the monitor, so another organization's incidents are never visible.
func List(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, f Filter) ([]Incident, int, error) {
	where := `m.organization_id = $1`
	args := []any{orgID}
	if f.id != nil {
		args = append(args, *f.id)
		where += fmt.Sprintf(" AND i.id = $%d", len(args))
	}
	add := func(cond string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.MonitorID != nil {
		add("i.monitor_id = $%d", *f.MonitorID)
	}
	if f.Status != "" {
		add("i.status = $%d", f.Status)
	}
	if f.From != nil {
		add("i.started_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("i.started_at < $%d", *f.To)
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM incidents i JOIN monitors m ON m.id = i.monitor_id WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := pool.Query(ctx, fmt.Sprintf(`SELECT i.id, i.monitor_id, m.name, i.started_at, i.resolved_at, i.status,
			i.cause_category, i.summary,
			EXTRACT(EPOCH FROM COALESCE(i.resolved_at, now()) - i.started_at)::bigint,
			(SELECT count(*) FROM incident_updates u WHERE u.incident_id = i.id)
		FROM incidents i JOIN monitors m ON m.id = i.monitor_id WHERE %s
		ORDER BY i.started_at DESC, i.id LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var in Incident
		if err := rows.Scan(&in.ID, &in.MonitorID, &in.MonitorName, &in.StartedAt, &in.ResolvedAt, &in.Status,
			&in.CauseCategory, &in.Summary, &in.DurationSeconds, &in.UpdateCount); err != nil {
			return nil, 0, err
		}
		out = append(out, in)
	}
	return out, total, rows.Err()
}

// Failures describes the failed checks of an incident, for writing about it: the monitor's kind, and the distinct HTTP status codes and check
// locations (region codes) of its failed checks between the start and the end (or now). At most 20 of each, most recent first.
func Failures(ctx context.Context, pool *pgxpool.Pool, orgID, incidentID uuid.UUID) (kind string, codes []int, regions []string, err error) {
	var monitorID uuid.UUID
	var started time.Time
	var resolved *time.Time
	err = pool.QueryRow(ctx, `SELECT m.id, m.kind, i.started_at, i.resolved_at FROM incidents i JOIN monitors m ON m.id = i.monitor_id
		WHERE i.id = $1 AND m.organization_id = $2`, incidentID, orgID).Scan(&monitorID, &kind, &started, &resolved)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil, ErrIncidentNotFound
	}
	if err != nil {
		return "", nil, nil, err
	}
	end := time.Now()
	if resolved != nil {
		end = *resolved
	}
	rows, err := pool.Query(ctx, `SELECT http_status_code, region, max(checked_at) AS last FROM check_results
		WHERE monitor_id = $1 AND NOT success AND checked_at >= $2 AND checked_at <= $3
		GROUP BY http_status_code, region ORDER BY last DESC LIMIT 40`, monitorID, started, end)
	if err != nil {
		return "", nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code *int16
		var region string
		var last time.Time
		if err := rows.Scan(&code, &region, &last); err != nil {
			return "", nil, nil, err
		}
		if code != nil && len(codes) < 20 && !slices.Contains(codes, int(*code)) {
			codes = append(codes, int(*code))
		}
		if region != "" && len(regions) < 20 && !slices.Contains(regions, region) {
			regions = append(regions, region)
		}
	}
	return kind, codes, regions, rows.Err()
}
