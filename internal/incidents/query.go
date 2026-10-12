package incidents

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Filter selects incidents of one organization.
type Filter struct {
	MonitorID *uuid.UUID
	Status    string     // open | resolved | ""
	From, To  *time.Time // bounds on started_at
	Limit     int
	Offset    int
}

// List returns one page of incidents (newest first) and the total count. Tenant scoping is by organization
// through the monitor, so another organization's incidents are never visible.
func List(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, f Filter) ([]Incident, int, error) {
	where := `m.organization_id = $1`
	args := []any{orgID}
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
			EXTRACT(EPOCH FROM COALESCE(i.resolved_at, now()) - i.started_at)::bigint
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
			&in.CauseCategory, &in.Summary, &in.DurationSeconds); err != nil {
			return nil, 0, err
		}
		out = append(out, in)
	}
	return out, total, rows.Err()
}
