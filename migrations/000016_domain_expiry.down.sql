DELETE FROM notification_deliveries WHERE event_type = 'domain_expiry';
DROP INDEX notification_deliveries_domain_idem;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_shape;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry'));
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_shape CHECK (
    (event_type = 'ssl_expiry' AND incident_id IS NULL AND monitor_id IS NOT NULL AND ssl_expires_at IS NOT NULL AND ssl_bucket IS NOT NULL)
    OR (event_type <> 'ssl_expiry' AND incident_id IS NOT NULL));
ALTER TABLE notification_deliveries DROP COLUMN domain_bucket, DROP COLUMN domain_expires_at, DROP COLUMN domain_id;
DROP TABLE domains;
ALTER TABLE plans DROP COLUMN max_domains;
