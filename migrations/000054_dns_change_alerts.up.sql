-- DNS change alerts (docs/plan-dns-change-alerts.md): each DNS monitor remembers, per check location, the answers it accepts as normal, and
-- alerts when they change. These are public DNS records of the customer's own domain; private addresses are never stored (they are refused).
ALTER TABLE monitors
    ADD COLUMN dns_change_alerts boolean NOT NULL DEFAULT true,   -- used by DNS monitors only; turning it off deletes the baselines
    ADD COLUMN dns_change_strict boolean NOT NULL DEFAULT false;  -- A/AAAA: alert on any change, not only on an address outside the 7-day pool

CREATE TABLE dns_baselines (
    monitor_id    uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    region        text NOT NULL,
    answers       text[] NOT NULL,                     -- the accepted set, sorted
    pending       text[],                              -- a different set seen at the last check, not yet confirmed
    pending_count smallint NOT NULL DEFAULT 0 CHECK (pending_count >= 0),
    last          text[] NOT NULL,                     -- the latest answers
    changed       boolean NOT NULL DEFAULT false,      -- this location has a confirmed change nobody has accepted
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (monitor_id, region),
    CHECK (cardinality(answers) <= 20 AND cardinality(last) <= 20 AND (pending IS NULL OR cardinality(pending) <= 20))
);

-- A/AAAA: the addresses seen at a location recently, so a CDN rotating through its pool isn't a change.
CREATE TABLE dns_seen (
    monitor_id uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    region     text NOT NULL,
    answer     text NOT NULL CHECK (char_length(answer) <= 1000),
    last_seen  timestamptz NOT NULL,
    PRIMARY KEY (monitor_id, region, answer)
);
CREATE INDEX dns_seen_last_seen_idx ON dns_seen (last_seen);

CREATE TABLE dns_changes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    monitor_id  uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    record_type text NOT NULL,
    before      text[] NOT NULL CHECK (cardinality(before) <= 20),
    after       text[] NOT NULL CHECK (cardinality(after) <= 20),
    detected_at timestamptz NOT NULL,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'reverted')),
    closed_at   timestamptz,
    accepted_by uuid REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT dns_changes_closed_consistent CHECK ((status = 'open') = (closed_at IS NULL))
);
-- At most one open change per monitor.
CREATE UNIQUE INDEX dns_changes_open_idx ON dns_changes (monitor_id) WHERE status = 'open';
CREATE INDEX dns_changes_monitor_idx ON dns_changes (monitor_id, detected_at DESC);

-- Alerts about a change: when it's confirmed, and when it closes (accepted, or changed back before anyone accepted it), like slowdowns.
ALTER TABLE notification_deliveries ADD COLUMN dns_change_id uuid REFERENCES dns_changes (id) ON DELETE CASCADE;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry', 'domain_expiry', 'slow', 'slow_recovery', 'dns_change', 'dns_change_closed'));
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
-- One alert per change, recipient and event.
CREATE UNIQUE INDEX notification_deliveries_dns_change_idem
    ON notification_deliveries (dns_change_id, notification_setting_id, event_type) WHERE dns_change_id IS NOT NULL;
