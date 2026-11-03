// Package notifications manages alert destinations and delivers alert emails reliably.
package notifications

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Event types.
const (
	EventOutage    = "outage"
	EventRecovery  = "recovery"
	EventSSLExpiry = "ssl_expiry"
	// EventDomainExpiry warns that a domain's registration is about to expire.
	EventDomainExpiry = "domain_expiry"
	// EventSlow: a monitor's responses have stayed above its slow-response threshold. EventSlowRecovery: they are back to normal.
	EventSlow         = "slow"
	EventSlowRecovery = "slow_recovery"
)

// hold turns "when the alert may go out" into the value stored: NULL (send now, as an alert that was never held) unless it is really later than now.
func hold(at time.Time) *time.Time {
	if at.After(time.Now()) {
		return &at
	}
	return nil
}

// Every Enqueue function runs inside the caller's transaction, so a problem and its notifications commit together, and queues one pending delivery
// per destination that routing picks for the monitor (routedRecipients). Unique keys make each idempotent: calling it again, or racing with another
// worker, can never create a second notification for the same event and destination. `at` is the earliest time it may be sent (an alert delay or the
// end of quiet hours); the dispatcher leaves it alone until then.

// Enqueue queues an incident's outage or recovery alert. A recovery is not sent to a destination whose outage alert was held and then canceled because
// the monitor recovered first: that destination never heard about the outage.
func Enqueue(ctx context.Context, tx pgx.Tx, orgID, monitorID, incidentID uuid.UUID, event string, at time.Time) (int, error) {
	tag, err := tx.Exec(ctx, routedRecipients+`
		INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type, next_attempt_at)
		SELECT $3, r.id, $4, COALESCE($5, now()) FROM recipients r
		WHERE $4 <> 'recovery' OR NOT EXISTS (SELECT 1 FROM notification_deliveries o WHERE o.incident_id = $3 AND o.notification_setting_id = r.id
			AND o.event_type = 'outage' AND o.suppressed = 'ended_while_held')
		ON CONFLICT (incident_id, notification_setting_id, event_type) WHERE incident_id IS NOT NULL DO NOTHING`,
		orgID, monitorID, incidentID, event, hold(at))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// EnqueueSSL queues a certificate-expiry warning. It is idempotent per monitor, recipient, certificate (its expiry date) and bucket.
func EnqueueSSL(ctx context.Context, tx pgx.Tx, orgID, monitorID uuid.UUID, expiresAt time.Time, bucket int, at time.Time) (int, error) {
	tag, err := tx.Exec(ctx, routedRecipients+`
		INSERT INTO notification_deliveries (monitor_id, notification_setting_id, event_type, ssl_expires_at, ssl_bucket, next_attempt_at)
		SELECT $2, r.id, $3, $4, $5, COALESCE($6, now()) FROM recipients r
		ON CONFLICT (monitor_id, notification_setting_id, ssl_expires_at, ssl_bucket) WHERE event_type = 'ssl_expiry' DO NOTHING`,
		orgID, monitorID, EventSSLExpiry, expiresAt, bucket, hold(at))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// EnqueueDomain queues a domain-expiry warning. A domain belongs to the account rather than to one monitor, so it goes to the destinations that
// receive every monitor's alerts (or the fallback). It is idempotent per domain, recipient, registration (its expiry date) and bucket.
func EnqueueDomain(ctx context.Context, tx pgx.Tx, orgID, domainID uuid.UUID, expiresAt time.Time, bucket int) (int, error) {
	tag, err := tx.Exec(ctx, routedRecipients+`
		INSERT INTO notification_deliveries (domain_id, notification_setting_id, event_type, domain_expires_at, domain_bucket)
		SELECT $3, r.id, $4, $5, $6 FROM recipients r
		ON CONFLICT (domain_id, notification_setting_id, domain_expires_at, domain_bucket) WHERE event_type = 'domain_expiry' DO NOTHING`,
		orgID, nil, domainID, EventDomainExpiry, expiresAt, bucket)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// EnqueueSlowdown queues a slow-response alert (EventSlow or EventSlowRecovery). Like a recovery, "back to normal" skips a destination whose slow alert
// was held and canceled because responses recovered first. It is idempotent per slowdown, recipient and event.
func EnqueueSlowdown(ctx context.Context, tx pgx.Tx, orgID, monitorID, slowdownID uuid.UUID, event string, at time.Time) (int, error) {
	tag, err := tx.Exec(ctx, routedRecipients+`
		INSERT INTO notification_deliveries (slowdown_id, notification_setting_id, event_type, next_attempt_at)
		SELECT $3, r.id, $4, COALESCE($5, now()) FROM recipients r
		WHERE $4 <> 'slow_recovery' OR NOT EXISTS (SELECT 1 FROM notification_deliveries o WHERE o.slowdown_id = $3 AND o.notification_setting_id = r.id
			AND o.event_type = 'slow' AND o.suppressed = 'ended_while_held')
		ON CONFLICT (slowdown_id, notification_setting_id, event_type) WHERE slowdown_id IS NOT NULL DO NOTHING`,
		orgID, monitorID, slowdownID, event, hold(at))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// CancelHeldOutage skips an incident's outage alerts that are still held (see cancelHeld). Call it when the incident resolves, before Enqueue(recovery).
func CancelHeldOutage(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) error {
	return cancelHeld(ctx, tx, "incident_id", incidentID, EventOutage)
}

// CancelHeldSlow skips a slowdown's slow alerts that are still held. Call it when the slowdown ends, before EnqueueSlowdown(slow_recovery).
func CancelHeldSlow(ctx context.Context, tx pgx.Tx, slowdownID uuid.UUID) error {
	return cancelHeld(ctx, tx, "slowdown_id", slowdownID, EventSlow)
}
