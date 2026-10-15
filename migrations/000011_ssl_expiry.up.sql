-- SSL certificate expiry tracking. The checker records when each HTTPS monitor's certificate expires; warnings are sent at 30, 14, 7 and 1 day(s)
-- before expiry and once more when it has expired (bucket 0).
ALTER TABLE monitors
    ADD COLUMN ssl_expires_at       timestamptz,
    ADD COLUMN ssl_checked_at       timestamptz,
    -- The smallest warning bucket already queued for the certificate that expires at ssl_notified_for; renewing the certificate resets it.
    ADD COLUMN ssl_notified_bucket  integer CHECK (ssl_notified_bucket IN (0, 1, 7, 14, 30)),
    ADD COLUMN ssl_notified_for     timestamptz;

-- Deliveries are no longer always about an incident: a certificate warning belongs to a monitor.
ALTER TABLE notification_deliveries
    ALTER COLUMN incident_id DROP NOT NULL,
    ADD COLUMN monitor_id      uuid REFERENCES monitors (id) ON DELETE CASCADE,
    ADD COLUMN ssl_expires_at  timestamptz,
    ADD COLUMN ssl_bucket      integer;

ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry'));
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_shape CHECK (
    (event_type = 'ssl_expiry' AND incident_id IS NULL AND monitor_id IS NOT NULL AND ssl_expires_at IS NOT NULL AND ssl_bucket IS NOT NULL)
    OR (event_type <> 'ssl_expiry' AND incident_id IS NOT NULL));

-- Idempotency: one incident notification per incident, recipient and event; one certificate warning per monitor, recipient, certificate and bucket.
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_idempotency;
CREATE UNIQUE INDEX notification_deliveries_incident_idem
    ON notification_deliveries (incident_id, notification_setting_id, event_type) WHERE incident_id IS NOT NULL;
CREATE UNIQUE INDEX notification_deliveries_ssl_idem
    ON notification_deliveries (monitor_id, notification_setting_id, ssl_expires_at, ssl_bucket) WHERE event_type = 'ssl_expiry';
