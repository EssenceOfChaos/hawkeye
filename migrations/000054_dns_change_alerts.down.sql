DELETE FROM notification_deliveries WHERE dns_change_id IS NOT NULL;
DROP INDEX notification_deliveries_dns_change_idem;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_shape;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_shape CHECK (
    (event_type = 'ssl_expiry' AND incident_id IS NULL AND monitor_id IS NOT NULL AND ssl_expires_at IS NOT NULL AND ssl_bucket IS NOT NULL
        AND domain_id IS NULL AND slowdown_id IS NULL)
    OR (event_type = 'domain_expiry' AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NOT NULL
        AND domain_expires_at IS NOT NULL AND domain_bucket IS NOT NULL AND slowdown_id IS NULL)
    OR (event_type IN ('outage', 'recovery') AND incident_id IS NOT NULL AND domain_id IS NULL AND slowdown_id IS NULL)
    OR (event_type IN ('slow', 'slow_recovery') AND slowdown_id IS NOT NULL AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NULL));
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry', 'domain_expiry', 'slow', 'slow_recovery'));
ALTER TABLE notification_deliveries DROP COLUMN dns_change_id;
DROP TABLE dns_changes;
DROP TABLE dns_seen;
DROP TABLE dns_baselines;
ALTER TABLE monitors DROP COLUMN dns_change_strict, DROP COLUMN dns_change_alerts;
