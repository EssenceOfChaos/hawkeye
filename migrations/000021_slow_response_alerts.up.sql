-- Slow-response alerts. A monitor may set a response-time threshold; when successful checks stay above it for `slow_checks` checks in a row, a
-- slowdown opens and alerts go out, and when they stay at or under it for as many checks it closes with a "back to normal" alert. A slowdown is
-- not an incident: the site is up, so it never counts against uptime and never appears on the public status page.
ALTER TABLE monitors
    ADD COLUMN slow_threshold_ms integer CHECK (slow_threshold_ms BETWEEN 100 AND 60000), -- NULL: slow-response alerts are off
    ADD COLUMN slow_checks       integer NOT NULL DEFAULT 3 CHECK (slow_checks BETWEEN 2 AND 10),
    -- Consecutive successful checks above / at or under the threshold (single-location monitors; see monitor_region_state for several).
    ADD COLUMN slow_streak       integer NOT NULL DEFAULT 0 CHECK (slow_streak >= 0),
    ADD COLUMN normal_streak     integer NOT NULL DEFAULT 0 CHECK (normal_streak >= 0);

-- With several locations each keeps its own streaks and verdict; the monitor is slow while most of its locations are.
ALTER TABLE monitor_region_state
    ADD COLUMN slow          boolean NOT NULL DEFAULT false,
    ADD COLUMN slow_streak   integer NOT NULL DEFAULT 0 CHECK (slow_streak >= 0),
    ADD COLUMN normal_streak integer NOT NULL DEFAULT 0 CHECK (normal_streak >= 0);

CREATE TABLE slowdowns (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    monitor_id   uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    started_at   timestamptz NOT NULL,       -- when the slowdown was confirmed
    resolved_at  timestamptz,
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    threshold_ms integer NOT NULL,           -- the threshold in force when it opened
    peak_ms      integer NOT NULL,           -- the slowest successful check seen while it was open
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT slowdowns_resolved_consistent CHECK ((status = 'open') = (resolved_at IS NULL)),
    CONSTRAINT slowdowns_resolved_after_start CHECK (resolved_at IS NULL OR resolved_at >= started_at)
);
-- At most one open slowdown per monitor, enforced by the database.
CREATE UNIQUE INDEX slowdowns_one_open_per_monitor ON slowdowns (monitor_id) WHERE status = 'open';
CREATE INDEX slowdowns_monitor_started_idx ON slowdowns (monitor_id, started_at DESC);

-- A slow-response alert belongs to a slowdown.
ALTER TABLE notification_deliveries ADD COLUMN slowdown_id uuid REFERENCES slowdowns (id) ON DELETE CASCADE;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry', 'domain_expiry', 'slow', 'slow_recovery'));
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_shape;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_shape CHECK (
    (event_type = 'ssl_expiry' AND incident_id IS NULL AND monitor_id IS NOT NULL AND ssl_expires_at IS NOT NULL AND ssl_bucket IS NOT NULL
        AND domain_id IS NULL AND slowdown_id IS NULL)
    OR (event_type = 'domain_expiry' AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NOT NULL
        AND domain_expires_at IS NOT NULL AND domain_bucket IS NOT NULL AND slowdown_id IS NULL)
    OR (event_type IN ('outage', 'recovery') AND incident_id IS NOT NULL AND domain_id IS NULL AND slowdown_id IS NULL)
    OR (event_type IN ('slow', 'slow_recovery') AND slowdown_id IS NOT NULL AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NULL));

-- One alert per slowdown, recipient and event.
CREATE UNIQUE INDEX notification_deliveries_slowdown_idem
    ON notification_deliveries (slowdown_id, notification_setting_id, event_type) WHERE slowdown_id IS NOT NULL;
