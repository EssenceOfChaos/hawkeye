CREATE TABLE incidents (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    monitor_id     uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    started_at     timestamptz NOT NULL,
    resolved_at    timestamptz,
    status         text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    cause_category text,
    summary        text CHECK (char_length(summary) <= 200),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT incidents_resolved_consistent CHECK ((status = 'open') = (resolved_at IS NULL)),
    CONSTRAINT incidents_resolved_after_start CHECK (resolved_at IS NULL OR resolved_at >= started_at)
);
-- At most one open incident per monitor, enforced by the database.
CREATE UNIQUE INDEX incidents_one_open_per_monitor ON incidents (monitor_id) WHERE status = 'open';
CREATE INDEX incidents_monitor_started_idx ON incidents (monitor_id, started_at DESC);
CREATE TRIGGER incidents_set_updated_at BEFORE UPDATE ON incidents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE notification_settings (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         uuid REFERENCES users (id) ON DELETE CASCADE,
    channel         text NOT NULL CHECK (channel IN ('email')),
    destination     text NOT NULL CHECK (destination = lower(btrim(destination)) AND char_length(destination) <= 254),
    enabled         boolean NOT NULL DEFAULT true,
    -- Alerts are only sent to verified destinations.
    verified_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    -- No duplicate recipients within an organization.
    CONSTRAINT notification_settings_unique_destination UNIQUE (organization_id, channel, destination)
);
CREATE TRIGGER notification_settings_set_updated_at BEFORE UPDATE ON notification_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Every existing organization owner receives alerts at their own address (verified iff their email is).
INSERT INTO notification_settings (organization_id, user_id, channel, destination, verified_at)
SELECT m.organization_id, u.id, 'email', u.email, u.email_verified_at
FROM organization_members m JOIN users u ON u.id = m.user_id WHERE m.role = 'owner';

CREATE TABLE notification_setting_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    setting_id uuid NOT NULL REFERENCES notification_settings (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_setting_tokens_setting_idx ON notification_setting_tokens (setting_id);

CREATE TABLE notification_deliveries (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id             uuid NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    notification_setting_id uuid NOT NULL REFERENCES notification_settings (id) ON DELETE CASCADE,
    event_type              text NOT NULL CHECK (event_type IN ('outage', 'recovery')),
    status                  text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'skipped')),
    attempt_count           integer NOT NULL DEFAULT 0,
    last_attempt_at         timestamptz,
    next_attempt_at         timestamptz NOT NULL DEFAULT now(),
    sent_at                 timestamptz,
    last_error              text CHECK (char_length(last_error) <= 200),
    created_at              timestamptz NOT NULL DEFAULT now(),
    -- Idempotency: one logical notification per incident, recipient and event, however often it is enqueued or retried.
    CONSTRAINT notification_deliveries_idempotency UNIQUE (incident_id, notification_setting_id, event_type)
);
CREATE INDEX notification_deliveries_due_idx ON notification_deliveries (next_attempt_at) WHERE status = 'pending';
