// Package notifications manages alert destinations and delivers alert emails reliably.
package notifications

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Event types.
const (
	EventOutage   = "outage"
	EventRecovery = "recovery"
)

// Enqueue records one pending delivery per enabled, verified destination of the organization.
// It runs inside the caller's transaction, so an incident and its notifications commit together.
// The unique key (incident, destination, event) makes it idempotent: calling it again, or racing
// with another worker, can never create a second notification for the same event.
func Enqueue(ctx context.Context, tx pgx.Tx, orgID, incidentID uuid.UUID, event string) (int, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type)
		SELECT $2, s.id, $3 FROM notification_settings s
		WHERE s.organization_id = $1 AND s.enabled AND s.verified_at IS NOT NULL
		ON CONFLICT (incident_id, notification_setting_id, event_type) DO NOTHING`, orgID, incidentID, event)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
