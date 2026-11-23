package checks

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/regions"
)

// LoadDNSLocations reads a monitor's change-alert state for every location that has one.
func LoadDNSLocations(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID) (map[string]DNSLocation, error) {
	out := map[string]DNSLocation{}
	rows, err := tx.Query(ctx, `SELECT region, answers, pending, pending_count, last, changed FROM dns_baselines WHERE monitor_id = $1`, monitorID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var region string
		var l DNSLocation
		if err := rows.Scan(&region, &l.Baseline, &l.Pending, &l.PendingCount, &l.Last, &l.Changed); err != nil {
			rows.Close()
			return nil, err
		}
		l.Seen = map[string]time.Time{}
		out[region] = l
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `SELECT region, answer, last_seen FROM dns_seen WHERE monitor_id = $1`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var region, answer string
		var at time.Time
		if err := rows.Scan(&region, &answer, &at); err != nil {
			return nil, err
		}
		if l, ok := out[region]; ok {
			l.Seen[answer] = at
		}
	}
	return out, rows.Err()
}

func loadDNSLocation(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, region string) (DNSLocation, error) {
	var l DNSLocation
	err := tx.QueryRow(ctx, `SELECT answers, pending, pending_count, last, changed FROM dns_baselines WHERE monitor_id = $1 AND region = $2`,
		monitorID, region).Scan(&l.Baseline, &l.Pending, &l.PendingCount, &l.Last, &l.Changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return DNSLocation{}, nil // no baseline yet
	}
	if err != nil {
		return DNSLocation{}, err
	}
	l.Seen = map[string]time.Time{}
	rows, err := tx.Query(ctx, `SELECT answer, last_seen FROM dns_seen WHERE monitor_id = $1 AND region = $2`, monitorID, region)
	if err != nil {
		return DNSLocation{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		var at time.Time
		if err := rows.Scan(&a, &at); err != nil {
			return DNSLocation{}, err
		}
		l.Seen[a] = at
	}
	return l, rows.Err()
}

// SaveDNSLocation writes a location's change-alert state, replacing its address pool.
func SaveDNSLocation(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, region string, l DNSLocation) error {
	last := l.Last
	if last == nil {
		last = l.Baseline
	}
	if _, err := tx.Exec(ctx, `INSERT INTO dns_baselines (monitor_id, region, answers, pending, pending_count, last, changed, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (monitor_id, region) DO UPDATE SET answers = EXCLUDED.answers, pending = EXCLUDED.pending, pending_count = EXCLUDED.pending_count,
			last = EXCLUDED.last, changed = EXCLUDED.changed, updated_at = EXCLUDED.updated_at`,
		monitorID, region, l.Baseline, l.Pending, l.PendingCount, last, l.Changed); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM dns_seen WHERE monitor_id = $1 AND region = $2`, monitorID, region); err != nil {
		return err
	}
	if len(l.Seen) == 0 {
		return nil
	}
	answers := make([]string, 0, len(l.Seen))
	seen := make([]time.Time, 0, len(l.Seen))
	for a, at := range l.Seen {
		answers, seen = append(answers, a), append(seen, at)
	}
	_, err := tx.Exec(ctx, `INSERT INTO dns_seen (monitor_id, region, answer, last_seen) SELECT $1, $2, a, s FROM unnest($3::text[], $4::timestamptz[]) AS x(a, s)`,
		monitorID, region, answers, seen)
	return err
}

// dnsSettings is what change alerts need to know about a DNS monitor, read with the monitor row.
type dnsSettings struct {
	recordType string // "" unless a DNS monitor
	on, strict bool
}

// recordDNS folds a DNS lookup's answers into the reporting location's state, and opens or closes the monitor's change: opened once most of its
// locations have confirmed a change, closed as reverted once none has any more. Runs inside the result's transaction.
func (s *Store) recordDNS(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, region string, effective []string, answers []string, d dnsSettings, at time.Time) error {
	loc, err := loadDNSLocation(ctx, tx, monitorID, region)
	if err != nil {
		return err
	}
	before := slices.Clone(loc.Baseline)
	next, step := FoldDNS(loc, answers, d.recordType, d.strict, at)
	if err := SaveDNSLocation(ctx, tx, monitorID, region, next); err != nil {
		return err
	}
	if s.incidents == nil || (step != DNSConfirmed && step != DNSBack) {
		return nil
	}
	var changed int
	var open bool
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM dns_baselines WHERE monitor_id = $1 AND region = ANY($2) AND changed),
			EXISTS (SELECT 1 FROM dns_changes WHERE monitor_id = $1 AND status = 'open')`, monitorID, effective).Scan(&changed, &open); err != nil {
		return err
	}
	switch {
	case step == DNSConfirmed && !open && changed >= regions.Quorum(len(effective)):
		return s.incidents.DNSChanged(ctx, tx, monitorID, d.recordType, before, answers, at)
	case step == DNSBack && open && changed == 0:
		return s.incidents.DNSChangeReverted(ctx, tx, monitorID, at)
	}
	return nil
}
