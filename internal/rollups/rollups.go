// Package rollups keeps hourly totals of check results (check_rollups) and lets readers combine them with raw results.
//
// Uptime, status pages, charts and reports used to add up every result in their range, which grows with the check interval and the history kept.
// A background Job adds up each closed UTC hour once. Readers use those totals for the whole hours before rollup_state.through, and raw results
// for the partial hours at either end of their range and for anything newer, so their answers are exactly what adding up raw results gives
// (results are never changed after they are recorded). If the job stops, readers simply read more raw results.
package rollups

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RawRetention is how long the individual results of a monitor checked more often than once a minute are kept (by internal/retention). Its
// hourly totals keep the plan's whole history, so uptime, status pages, charts and reports still cover it; only the list of individual checks is
// shorter. At 30-second checks a full Agency account's year of results would be 44 GB; this keeps it near 4 GB (docs/plan-sub-minute-checks.md).
const RawRetention = 30 * 24 * time.Hour

// SubMinute is the check interval, in seconds, below which RawRetention applies.
const SubMinute = 60

// DB is satisfied by *pgxpool.Pool and pgx.Tx.
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Through is the time before which every hour's totals are complete, or the zero time when none are.
func Through(ctx context.Context, db DB) (time.Time, error) {
	var t *time.Time
	if err := db.QueryRow(ctx, `SELECT through FROM rollup_state`).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, nil
	}
	return t.UTC(), nil
}

// Span returns the part [a, b) of [from, to) that rollups cover: whole UTC hours, ending no later than through. a and b are always within
// [from, to] with a <= b, so the raw parts [from, a) and [b, to) never reach outside the range; a == b means none (read raw results only).
func Span(from, to, through time.Time) (a, b time.Time) {
	from, to = from.UTC(), to.UTC()
	a = from.Truncate(time.Hour)
	if a.Before(from) {
		a = a.Add(time.Hour)
	}
	if a.After(to) {
		a = to
	}
	b = to.Truncate(time.Hour)
	if through.Before(b) { // including the zero time: no totals yet
		b = through.UTC()
	}
	if b.Before(a) {
		b = a
	}
	return a, b
}

// SpanFor reads rollup_state and returns Span for [from, to).
func SpanFor(ctx context.Context, db DB, from, to time.Time) (a, b time.Time, err error) {
	through, err := Through(ctx, db)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	a, b = Span(from, to, through)
	return a, b, nil
}

// Source is SQL for a relation over the results of the monitors whose ids the subquery `monitors` returns, checked in [from, to):
//
//	monitor_id, t, in_maintenance, checks, successes, ms_sum, ms_count, ms_max,
//	dns_ms_sum, connect_ms_sum, tls_ms_sum, wait_ms_sum, phase_count
//
// Between a and b each row is one rollup (t is its hour); outside, each row is one result (checks = 1). Sum the counts, divide ms_sum by ms_count
// for an average response time over successful checks, and take max(ms_max). Divide each phase's sum by phase_count for the average timing
// breakdown over the successful checks that have one. from, to, a and b are SQL parameter references such as "$2", with
// a and b from Span. Use it as `FROM (<Source>) u`. Immediate re-checks are left out, as they are from the rollups: uptime and response times count
// one result per interval.
func Source(monitors, from, to, a, b string) string {
	raw := func(lo, hi string) string {
		return fmt.Sprintf(`SELECT monitor_id, checked_at AS t, in_maintenance, 1 AS checks, success::int AS successes,
			CASE WHEN success AND response_time_ms IS NOT NULL THEN response_time_ms ELSE 0 END::bigint AS ms_sum,
			(success AND response_time_ms IS NOT NULL)::int AS ms_count, CASE WHEN success THEN response_time_ms END AS ms_max,
			CASE WHEN success THEN COALESCE(dns_ms, 0) ELSE 0 END::bigint AS dns_ms_sum, CASE WHEN success THEN COALESCE(connect_ms, 0) ELSE 0 END::bigint AS connect_ms_sum,
			CASE WHEN success THEN COALESCE(tls_ms, 0) ELSE 0 END::bigint AS tls_ms_sum, CASE WHEN success THEN COALESCE(wait_ms, 0) ELSE 0 END::bigint AS wait_ms_sum,
			(success AND wait_ms IS NOT NULL)::int AS phase_count
			FROM check_results WHERE monitor_id IN (%s) AND checked_at >= %s AND checked_at < %s AND NOT recheck`, monitors, lo, hi)
	}
	return raw(from, a) + "\n\t\tUNION ALL " + raw(b, to) + fmt.Sprintf(`
		UNION ALL SELECT monitor_id, hour, in_maintenance, checks, successes, ms_sum, ms_count, ms_max,
			dns_ms_sum, connect_ms_sum, tls_ms_sum, wait_ms_sum, phase_count
			FROM check_rollups WHERE monitor_id IN (%s) AND hour >= %s AND hour < %s`, monitors, a, b)
}
