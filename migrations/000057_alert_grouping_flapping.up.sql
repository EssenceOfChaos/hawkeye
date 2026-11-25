-- Alert grouping and flap suppression (docs/plan-alert-grouping.md).

-- Grouping: when several alerts of one kind reach one destination close together, the first goes out at once and the rest are sent as one message
-- (internal/notifications/dispatcher.go). On by default; the owner can turn it off for the organization.
ALTER TABLE organizations ADD COLUMN alert_grouping boolean NOT NULL DEFAULT true;
-- group_held: the dispatcher held this alert back to send it with others. Such an alert is always sent, even if its problem ends first:
-- grouping changes how alerts are packaged, never whether they are sent (cancelHeld skips it).
ALTER TABLE notification_deliveries ADD COLUMN group_held boolean NOT NULL DEFAULT false;
-- "Was something of this kind sent to this destination in the last few minutes?"
CREATE INDEX notification_deliveries_recent_idx ON notification_deliveries (notification_setting_id, event_type, last_attempt_at)
    WHERE last_attempt_at IS NOT NULL;

-- Flap suppression: a monitor that keeps going down and coming back gets one "flapping" alert instead of an alert for every outage and recovery.
-- On by default per monitor.
ALTER TABLE monitors ADD COLUMN flap_suppression boolean NOT NULL DEFAULT true;

-- An episode opens on the outage that makes a monitor flapping, and closes when it settles (stayed up), stays down (then it is alerted as an
-- ordinary outage) or ends because flap suppression was turned off or the monitor paused.
CREATE TABLE flap_episodes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    monitor_id uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL,
    ended_at   timestamptz,
    status     text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'settled', 'stayed_down', 'ended')),
    -- Outages counted, including the ones in the hour before that made it flapping.
    outages    integer NOT NULL CHECK (outages >= 1),
    CONSTRAINT flap_episodes_closed_consistent CHECK ((status = 'open') = (ended_at IS NULL)),
    CONSTRAINT flap_episodes_ended_after_start CHECK (ended_at IS NULL OR ended_at >= started_at)
);
-- At most one open episode per monitor.
CREATE UNIQUE INDEX flap_episodes_one_open ON flap_episodes (monitor_id) WHERE status = 'open';
CREATE INDEX flap_episodes_monitor_idx ON flap_episodes (monitor_id, started_at DESC);

-- An incident opened while its monitor was flapping. alerts_paused: its own outage and recovery alerts are not sent (the episode's alerts cover
-- it), until the episode ends with the monitor still down; then its outage is alerted and the flag is cleared.
ALTER TABLE incidents
    ADD COLUMN flap_episode_id uuid REFERENCES flap_episodes (id) ON DELETE SET NULL,
    ADD COLUMN alerts_paused boolean NOT NULL DEFAULT false;
CREATE INDEX incidents_flap_episode_idx ON incidents (flap_episode_id) WHERE flap_episode_id IS NOT NULL;

-- The episode's alerts: "flapping" when it starts, "flapping_ended" when it settles or ends.
ALTER TABLE notification_deliveries ADD COLUMN flap_episode_id uuid REFERENCES flap_episodes (id) ON DELETE CASCADE;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry', 'domain_expiry', 'slow', 'slow_recovery', 'dns_change', 'dns_change_closed',
        'flapping', 'flapping_ended'));
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_shape;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_shape CHECK (
    (event_type = 'ssl_expiry' AND incident_id IS NULL AND monitor_id IS NOT NULL AND ssl_expires_at IS NOT NULL AND ssl_bucket IS NOT NULL
        AND domain_id IS NULL AND slowdown_id IS NULL AND dns_change_id IS NULL AND flap_episode_id IS NULL)
    OR (event_type = 'domain_expiry' AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NOT NULL
        AND domain_expires_at IS NOT NULL AND domain_bucket IS NOT NULL AND slowdown_id IS NULL AND dns_change_id IS NULL AND flap_episode_id IS NULL)
    OR (event_type IN ('outage', 'recovery') AND incident_id IS NOT NULL AND domain_id IS NULL AND slowdown_id IS NULL AND dns_change_id IS NULL
        AND flap_episode_id IS NULL)
    OR (event_type IN ('slow', 'slow_recovery') AND slowdown_id IS NOT NULL AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NULL
        AND dns_change_id IS NULL AND flap_episode_id IS NULL)
    OR (event_type IN ('dns_change', 'dns_change_closed') AND dns_change_id IS NOT NULL AND incident_id IS NULL AND monitor_id IS NULL
        AND domain_id IS NULL AND slowdown_id IS NULL AND flap_episode_id IS NULL)
    OR (event_type IN ('flapping', 'flapping_ended') AND flap_episode_id IS NOT NULL AND incident_id IS NULL AND monitor_id IS NULL
        AND domain_id IS NULL AND slowdown_id IS NULL AND dns_change_id IS NULL));
-- One alert per episode, recipient and event.
CREATE UNIQUE INDEX notification_deliveries_flap_idem
    ON notification_deliveries (flap_episode_id, notification_setting_id, event_type) WHERE flap_episode_id IS NOT NULL;
