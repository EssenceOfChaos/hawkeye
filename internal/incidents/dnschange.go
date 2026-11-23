package incidents

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/notifications"
)

// DNS change alerts (docs/plan-dns-change-alerts.md). A change is not an outage: it is handled like a slowdown, with one alert when it is
// confirmed and one when it closes, either accepted ("this change was intended") or changed back before anyone did.

var ErrDNSChangeNotFound = errors.New("dns change not found")

// DNSChange is a DNS change as the API shows it.
type DNSChange struct {
	ID         uuid.UUID  `json:"id"`
	RecordType string     `json:"record_type"`
	Before     []string   `json:"before"`
	After      []string   `json:"after"`
	Summary    string     `json:"summary"` // "mail.example.com → mx.other.net", or "1 answer added"
	DetectedAt time.Time  `json:"detected_at"`
	Status     string     `json:"status"` // open, accepted or reverted
	ClosedAt   *time.Time `json:"closed_at"`
	AcceptedBy *string    `json:"accepted_by"` // the email address of whoever accepted it
}

// DNSChanged opens a change for the monitor and queues its alert (quiet hours apply; the alert delay doesn't, since a change is already
// confirmed by several lookups). At most one change is open per monitor; a second call is a no-op.
func (Engine) DNSChanged(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, recordType string, before, after []string, at time.Time) error {
	var id, orgID uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO dns_changes (monitor_id, record_type, before, after, detected_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (monitor_id) WHERE status = 'open' DO NOTHING
		RETURNING id, (SELECT organization_id FROM monitors WHERE id = $1)`, monitorID, recordType, before, after, at).Scan(&id, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	timing, err := notifications.MonitorTiming(ctx, tx, monitorID)
	if err != nil {
		return err
	}
	_, err = notifications.EnqueueDNSChange(ctx, tx, orgID, monitorID, id, notifications.EventDNSChange, timing.At(time.Now()))
	return err
}

// DNSChangeReverted closes the monitor's open change because the record is back to what it was. A change alert still held in quiet hours is
// canceled instead, and that destination gets no follow-up.
func (Engine) DNSChangeReverted(ctx context.Context, tx pgx.Tx, monitorID uuid.UUID, at time.Time) error {
	return closeChange(ctx, tx, monitorID, uuid.Nil, "reverted", nil, at)
}

// closeChange closes the monitor's open change (changeID, when set, must be it) as accepted or reverted, and queues the follow-up alert.
func closeChange(ctx context.Context, tx pgx.Tx, monitorID, changeID uuid.UUID, status string, by *uuid.UUID, at time.Time) error {
	var id, orgID uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE dns_changes c SET status = $3, closed_at = GREATEST(c.detected_at, $4), accepted_by = $5
		FROM monitors m WHERE c.monitor_id = $1 AND c.status = 'open' AND ($2 = '00000000-0000-0000-0000-000000000000'::uuid OR c.id = $2)
			AND m.id = c.monitor_id
		RETURNING c.id, m.organization_id`, monitorID, changeID, status, at, by).Scan(&id, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDNSChangeNotFound
	}
	if err != nil {
		return err
	}
	if err := notifications.CancelHeldDNSChange(ctx, tx, id); err != nil {
		return err
	}
	timing, err := notifications.MonitorTiming(ctx, tx, monitorID)
	if err != nil {
		return err
	}
	_, err = notifications.EnqueueDNSChange(ctx, tx, orgID, monitorID, id, notifications.EventDNSChangeClosed, timing.At(time.Now()))
	return err
}

// AcceptDNSChange records that an open change was intended: every location's latest answers become its baseline, the change closes as accepted
// (resolving its PagerDuty warning), and the team is told who accepted it. The monitor row is locked first, as the check recorder does, so a
// result can't be folded into a baseline that is being replaced.
func AcceptDNSChange(ctx context.Context, pool *pgxpool.Pool, orgID, monitorID, changeID, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var recordType string
		var strict bool
		err := tx.QueryRow(ctx, `SELECT COALESCE(dns_record_type, ''), dns_change_strict FROM monitors
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL FOR NO KEY UPDATE`, monitorID, orgID).Scan(&recordType, &strict)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDNSChangeNotFound
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if err := closeChange(ctx, tx, monitorID, changeID, "accepted", &userID, now); err != nil {
			return err
		}
		locs, err := checks.LoadDNSLocations(ctx, tx, monitorID)
		if err != nil {
			return err
		}
		for region, loc := range locs {
			if err := checks.SaveDNSLocation(ctx, tx, monitorID, region, checks.AcceptDNS(loc, recordType, strict, now)); err != nil {
				return err
			}
		}
		return nil
	})
}

// DNSChanges lists a monitor's changes, newest first.
func DNSChanges(ctx context.Context, pool *pgxpool.Pool, orgID, monitorID uuid.UUID, limit int) ([]DNSChange, error) {
	rows, err := pool.Query(ctx, `SELECT c.id, c.record_type, c.before, c.after, c.detected_at, c.status, c.closed_at, u.email
		FROM dns_changes c JOIN monitors m ON m.id = c.monitor_id LEFT JOIN users u ON u.id = c.accepted_by
		WHERE c.monitor_id = $1 AND m.organization_id = $2 ORDER BY c.detected_at DESC LIMIT $3`, monitorID, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DNSChange{}
	for rows.Next() {
		var c DNSChange
		if err := rows.Scan(&c.ID, &c.RecordType, &c.Before, &c.After, &c.DetectedAt, &c.Status, &c.ClosedAt, &c.AcceptedBy); err != nil {
			return nil, err
		}
		c.Summary = checks.DescribeDNSChange(c.Before, c.After)
		out = append(out, c)
	}
	return out, rows.Err()
}

// DNSCurrent is the record's latest answers as HawkEye sees them, from the location that looked it up most recently; nil before the first
// lookup or with change alerts off.
func DNSCurrent(ctx context.Context, pool *pgxpool.Pool, monitorID uuid.UUID) ([]string, error) {
	var out []string
	err := pool.QueryRow(ctx, `SELECT last FROM dns_baselines WHERE monitor_id = $1 ORDER BY updated_at DESC LIMIT 1`, monitorID).Scan(&out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return out, err
}
