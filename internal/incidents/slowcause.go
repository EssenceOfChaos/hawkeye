package incidents

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/checks"
	"hawkeye/internal/notifications"
	"hawkeye/internal/rollups"
)

// baselineChecks is how many successful checks with a timing breakdown the previous week must have before a slowdown is explained against
// it: with fewer, "usually" would mean little.
const baselineChecks = 10

// phase is one part of a check's time, as a slowdown explanation names it.
type phase struct {
	name       string // completes "Most of the extra time is ..."
	slow, base int    // ms: the slow check's, and the usual average
}

// SlowCause explains a slowdown from the slow check's timing breakdown and the monitor's usual one: the phase that accounts for at least half
// of the extra time, or that the extra time is spread out. Empty when there is nothing to compare (no breakdown, too little history, or no
// phase slower than usual).
func SlowCause(slow *checks.Timings, usual *Baseline) string {
	if slow == nil || usual == nil || usual.Checks < baselineChecks {
		return ""
	}
	phases := []phase{
		{"the DNS lookup", slow.DNSMs, usual.DNSMs},
		{"connecting to the server", slow.ConnectMs, usual.ConnectMs},
		{"the TLS handshake", slow.TLSMs, usual.TLSMs},
		{"the server's wait for a first byte", slow.WaitMs, usual.WaitMs},
	}
	extra, top := 0, phases[0]
	for _, p := range phases {
		if d := p.slow - p.base; d > 0 {
			extra += d
			if d > top.slow-top.base {
				top = p
			}
		}
	}
	if extra <= 0 {
		return ""
	}
	if 2*(top.slow-top.base) >= extra {
		return "Most of the extra time is " + top.name + ": " + notifications.Milliseconds(top.slow) + ", against " +
			notifications.Milliseconds(top.base) + " usually."
	}
	return "The extra time is spread across the connection: no single step (DNS lookup, connecting, TLS handshake or the server's wait) " +
		"accounts for most of it."
}

// Baseline is a monitor's average timing breakdown over its successful checks.
type Baseline struct {
	DNSMs, ConnectMs, TLSMs, WaitMs, Checks int
}

// usualTimings is the monitor's average timing breakdown over the week before its slow streak began (the slow checks themselves would pull
// "usually" toward the slowdown), from the hourly totals where they exist so it stays cheap. nil when nothing was measured.
func usualTimings(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, at time.Time) (*Baseline, error) {
	until := at
	if err := tx.QueryRow(ctx, `SELECT COALESCE(min(checked_at), $2) FROM (SELECT checked_at FROM check_results
			WHERE monitor_id = $1 AND success AND NOT recheck AND checked_at <= $2
			ORDER BY checked_at DESC LIMIT (SELECT slow_checks FROM monitors WHERE id = $1)) s`, monitorID, at).Scan(&until); err != nil {
		return nil, err
	}
	from := until.Add(-7 * 24 * time.Hour)
	a, b, err := rollups.SpanFor(ctx, tx, from, until)
	if err != nil {
		return nil, err
	}
	var bl Baseline
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(phase_count), 0)::int,
			COALESCE(round(sum(dns_ms_sum)::numeric / NULLIF(sum(phase_count), 0)), 0)::int,
			COALESCE(round(sum(connect_ms_sum)::numeric / NULLIF(sum(phase_count), 0)), 0)::int,
			COALESCE(round(sum(tls_ms_sum)::numeric / NULLIF(sum(phase_count), 0)), 0)::int,
			COALESCE(round(sum(wait_ms_sum)::numeric / NULLIF(sum(phase_count), 0)), 0)::int
		FROM (`+rollups.Source(`SELECT $1::uuid`, "$2", "$3", "$4", "$5")+`) u WHERE NOT in_maintenance`, monitorID, from, until, a, b).
		Scan(&bl.Checks, &bl.DNSMs, &bl.ConnectMs, &bl.TLSMs, &bl.WaitMs)
	if err != nil || bl.Checks == 0 {
		return nil, err
	}
	return &bl, nil
}
