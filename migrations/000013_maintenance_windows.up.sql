-- Maintenance windows: while one is active for a monitor, its checks still run and are recorded, but they do not
-- change its status, open or resolve incidents, send alerts, or count towards uptime.
ALTER TABLE plans ADD COLUMN maintenance_enabled boolean NOT NULL DEFAULT false;
UPDATE plans SET maintenance_enabled = true WHERE code IN ('pro', 'agency');

CREATE TABLE maintenance_windows (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name             text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    -- The first (or only) occurrence. Repeats are exact multiples of 24 h or 7 d after it, in UTC.
    starts_at        timestamptz NOT NULL,
    duration_minutes integer NOT NULL CHECK (duration_minutes BETWEEN 1 AND 10080),
    repeat           text NOT NULL DEFAULT 'none' CHECK (repeat IN ('none', 'daily', 'weekly')),
    -- All of the organization's monitors (including ones added later), or only those in maintenance_window_monitors.
    all_monitors     boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- A repeating window must be shorter than its period, or it would never end.
    CONSTRAINT maintenance_windows_shorter_than_period CHECK (
        (repeat = 'none') OR (repeat = 'daily' AND duration_minutes < 1440) OR (repeat = 'weekly' AND duration_minutes < 10080))
);
CREATE INDEX maintenance_windows_org_idx ON maintenance_windows (organization_id);

CREATE TABLE maintenance_window_monitors (
    window_id  uuid NOT NULL REFERENCES maintenance_windows (id) ON DELETE CASCADE,
    monitor_id uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    PRIMARY KEY (window_id, monitor_id)
);
CREATE INDEX maintenance_window_monitors_monitor_idx ON maintenance_window_monitors (monitor_id);

-- Whether the window covers time p_at (ignoring which monitors it applies to).
CREATE FUNCTION maintenance_window_active(w maintenance_windows, p_at timestamptz) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT p_at >= w.starts_at AND CASE w.repeat
        WHEN 'none'  THEN p_at < w.starts_at + make_interval(mins => w.duration_minutes)
        WHEN 'daily' THEN mod(floor(extract(epoch FROM p_at - w.starts_at))::bigint, 86400)  < w.duration_minutes * 60
        ELSE              mod(floor(extract(epoch FROM p_at - w.starts_at))::bigint, 604800) < w.duration_minutes * 60
    END
$$;

-- Whether any window covers the monitor at p_at. The organization is passed in so the answer does not depend on reading the monitors table.
CREATE FUNCTION in_maintenance(p_org uuid, p_monitor uuid, p_at timestamptz) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM maintenance_windows w
        WHERE w.organization_id = p_org AND maintenance_window_active(w, p_at)
          AND (w.all_monitors OR EXISTS (SELECT 1 FROM maintenance_window_monitors wm WHERE wm.window_id = w.id AND wm.monitor_id = p_monitor)))
$$;

ALTER TABLE check_results ADD COLUMN in_maintenance boolean NOT NULL DEFAULT false;
