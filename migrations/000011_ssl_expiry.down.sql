DELETE FROM notification_deliveries WHERE event_type = 'ssl_expiry';
DROP INDEX notification_deliveries_ssl_idem;
DROP INDEX notification_deliveries_incident_idem;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_idempotency UNIQUE (incident_id, notification_setting_id, event_type);
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_shape;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check CHECK (event_type IN ('outage', 'recovery'));
ALTER TABLE notification_deliveries
    DROP COLUMN ssl_bucket, DROP COLUMN ssl_expires_at, DROP COLUMN monitor_id,
    ALTER COLUMN incident_id SET NOT NULL;
ALTER TABLE monitors
    DROP COLUMN ssl_notified_for, DROP COLUMN ssl_notified_bucket, DROP COLUMN ssl_checked_at, DROP COLUMN ssl_expires_at;
