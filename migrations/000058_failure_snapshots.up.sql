-- Rich failure snapshots (docs/plan-failure-snapshots.md): what HawkEye saw when an outage was confirmed, and what changed since the last good check.

-- The last good response each location saw of each monitor (checks.ResponseFacts: allowlisted headers, size, server address, certificate,
-- timings; for a multi-step check an array, one per step), and its fingerprint. Per location, because CDNs and geo-DNS answer each location
-- differently: a failure is compared with what the same location saw when it worked. Written only when the fingerprint changes, so a steady site
-- writes it about once per location. Never response content.
CREATE TABLE monitor_last_good (
    monitor_id  uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    region      text NOT NULL,
    facts       jsonb NOT NULL,
    fingerprint text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (monitor_id, region)
);

-- Opt-in: keep the first 4 KB of a failed response's body with the incident, encrypted, shown in the app only. Off by default.
ALTER TABLE monitors ADD COLUMN capture_failure_body boolean NOT NULL DEFAULT false;

-- One per incident, from the check that confirmed the outage. Deleted with its incident by retention.
CREATE TABLE incident_snapshots (
    incident_id     uuid PRIMARY KEY REFERENCES incidents (id) ON DELETE CASCADE,
    checked_at      timestamptz NOT NULL,
    region          text NOT NULL,
    facts           jsonb NOT NULL,              -- checks.ResponseFacts of the failing check, or of the failing step
    step_index      smallint CHECK (step_index >= 0),
    step_name       text,
    changes         jsonb NOT NULL DEFAULT '[]', -- checks.Change list, worked out once, when the outage was confirmed
    locations       jsonb,                       -- multi-location monitors: what each location last saw
    -- Sealed with the secrets box (context: the incident ID). NULL unless the monitor opted in and a response arrived, or once cleared.
    body_excerpt    text,
    -- Whether the monitor asked for the body when the outage was confirmed, so the app can tell "not kept" from "nothing to keep".
    body_requested  boolean NOT NULL DEFAULT false,
    body_truncated  boolean NOT NULL DEFAULT false,
    body_binary     boolean NOT NULL DEFAULT false,
    -- Set when retention removed the excerpt, 30 days after the incident resolved.
    body_cleared_at timestamptz,
    CONSTRAINT incident_snapshots_step CHECK ((step_index IS NULL) = (step_name IS NULL))
);
-- Retention looks for excerpts still held.
CREATE INDEX incident_snapshots_excerpt_idx ON incident_snapshots (incident_id) WHERE body_excerpt IS NOT NULL;
