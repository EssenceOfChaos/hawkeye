DROP INDEX notification_deliveries_slowdown_idem;
DELETE FROM notification_deliveries WHERE event_type IN ('slow', 'slow_recovery');
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_shape;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_shape CHECK (
    (event_type = 'ssl_expiry' AND incident_id IS NULL AND monitor_id IS NOT NULL AND ssl_expires_at IS NOT NULL AND ssl_bucket IS NOT NULL
        AND domain_id IS NULL)
    OR (event_type = 'domain_expiry' AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NOT NULL
        AND domain_expires_at IS NOT NULL AND domain_bucket IS NOT NULL)
    OR (event_type IN ('outage', 'recovery') AND incident_id IS NOT NULL AND domain_id IS NULL));
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry', 'domain_expiry'));
ALTER TABLE notification_deliveries DROP COLUMN slowdown_id;
DROP TABLE slowdowns;
ALTER TABLE monitor_region_state DROP COLUMN slow, DROP COLUMN slow_streak, DROP COLUMN normal_streak;
ALTER TABLE monitors DROP COLUMN slow_threshold_ms, DROP COLUMN slow_checks, DROP COLUMN slow_streak, DROP COLUMN normal_streak;
