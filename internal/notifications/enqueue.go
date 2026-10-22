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
)

// Enqueue records one pending delivery per enabled, verified destination of the organization.
// It runs inside the caller's transaction, so an incident and its notifications commit together.
// The unique key (incident, destination, event) makes it idempotent: calling it again, or racing
// with another worker, can never create a second notification for the same event.
func Enqueue(ctx context.Context, tx pgx.Tx, orgID, incidentID uuid.UUID, event string) (int, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type)
		SELECT $2, s.id, $3 FROM notification_settings s
		WHERE s.organization_id = $1 AND s.enabled AND s.verified_at IS NOT NULL
		ON CONFLICT (incident_id, notification_setting_id, event_type) WHERE incident_id IS NOT NULL DO NOTHING`, orgID, incidentID, event)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// EnqueueSSL queues a certificate-expiry warning for every enabled, verified recipient of the organization. It is idempotent per monitor,
// recipient, certificate (its expiry date) and bucket, so a retry can never send the same warning twice.
func EnqueueSSL(ctx context.Context, tx pgx.Tx, orgID, monitorID uuid.UUID, expiresAt time.Time, bucket int) (int, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO notification_deliveries (monitor_id, notification_setting_id, event_type, ssl_expires_at, ssl_bucket)
		SELECT $2, s.id, $3, $4, $5 FROM notification_settings s
		WHERE s.organization_id = $1 AND s.enabled AND s.verified_at IS NOT NULL
		ON CONFLICT (monitor_id, notification_setting_id, ssl_expires_at, ssl_bucket) WHERE event_type = 'ssl_expiry' DO NOTHING`,
		orgID, monitorID, EventSSLExpiry, expiresAt, bucket)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// EnqueueDomain queues a domain-expiry warning for every enabled, verified recipient of the organization. It is idempotent per domain, recipient,
// registration (its expiry date) and bucket, so a retry can never send the same warning twice.
func EnqueueDomain(ctx context.Context, tx pgx.Tx, orgID, domainID uuid.UUID, expiresAt time.Time, bucket int) (int, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO notification_deliveries (domain_id, notification_setting_id, event_type, domain_expires_at, domain_bucket)
		SELECT $2, s.id, $3, $4, $5 FROM notification_settings s
		WHERE s.organization_id = $1 AND s.enabled AND s.verified_at IS NOT NULL
		ON CONFLICT (domain_id, notification_setting_id, domain_expires_at, domain_bucket) WHERE event_type = 'domain_expiry' DO NOTHING`,
		orgID, domainID, EventDomainExpiry, expiresAt, bucket)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
