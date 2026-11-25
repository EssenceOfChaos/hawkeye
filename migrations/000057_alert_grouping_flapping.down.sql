DELETE FROM notification_deliveries WHERE flap_episode_id IS NOT NULL;
DROP INDEX notification_deliveries_flap_idem;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_shape;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_shape CHECK (
    (event_type = 'ssl_expiry' AND incident_id IS NULL AND monitor_id IS NOT NULL AND ssl_expires_at IS NOT NULL AND ssl_bucket IS NOT NULL
        AND domain_id IS NULL AND slowdown_id IS NULL AND dns_change_id IS NULL)
    OR (event_type = 'domain_expiry' AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NOT NULL
        AND domain_expires_at IS NOT NULL AND domain_bucket IS NOT NULL AND slowdown_id IS NULL AND dns_change_id IS NULL)
    OR (event_type IN ('outage', 'recovery') AND incident_id IS NOT NULL AND domain_id IS NULL AND slowdown_id IS NULL AND dns_change_id IS NULL)
    OR (event_type IN ('slow', 'slow_recovery') AND slowdown_id IS NOT NULL AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NULL
        AND dns_change_id IS NULL)
    OR (event_type IN ('dns_change', 'dns_change_closed') AND dns_change_id IS NOT NULL AND incident_id IS NULL AND monitor_id IS NULL
        AND domain_id IS NULL AND slowdown_id IS NULL));
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry', 'domain_expiry', 'slow', 'slow_recovery', 'dns_change', 'dns_change_closed'));
ALTER TABLE notification_deliveries DROP COLUMN flap_episode_id;
DROP INDEX incidents_flap_episode_idx;
ALTER TABLE incidents DROP COLUMN alerts_paused, DROP COLUMN flap_episode_id;
DROP TABLE flap_episodes;
ALTER TABLE monitors DROP COLUMN flap_suppression;
DROP INDEX notification_deliveries_recent_idx;
ALTER TABLE notification_deliveries DROP COLUMN group_held;
ALTER TABLE organizations DROP COLUMN alert_grouping;
