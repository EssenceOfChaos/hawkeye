-- Deploys (docs/plan-deploy-maintenance.md): a customer's CI quiets alerts while it deploys, then asks HawkEye to check the site. A deploy is a
-- maintenance window of kind 'deploy' that starts now, ends by itself after its minutes, and ends early when the pipeline finishes it.
ALTER TABLE maintenance_windows
    ADD COLUMN kind text NOT NULL DEFAULT 'scheduled' CHECK (kind IN ('scheduled', 'deploy')),
    ADD COLUMN ended_at timestamptz,                                   -- finished early (deploys only)
    ADD COLUMN note text CHECK (char_length(note) <= 200),             -- a commit SHA, version or pipeline URL
    ADD COLUMN target text CHECK (char_length(target) <= 100),         -- what the deploy named: a tag's name, or NULL for every monitor or a list
    ADD COLUMN started_by_token uuid REFERENCES api_tokens (id) ON DELETE SET NULL,
    ADD COLUMN verified boolean,                                       -- the last finish check passed (true) or failed (false); NULL if none ran
    ADD CONSTRAINT maintenance_windows_ended_only_deploys CHECK (ended_at IS NULL OR kind = 'deploy');
CREATE INDEX maintenance_windows_deploys_idx ON maintenance_windows (organization_id, created_at) WHERE kind = 'deploy';

-- A window stops covering anything once it has ended early.
CREATE OR REPLACE FUNCTION maintenance_window_active(w maintenance_windows, p_at timestamptz) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT p_at >= w.starts_at AND (w.ended_at IS NULL OR p_at < w.ended_at) AND CASE w.repeat
        WHEN 'none'  THEN p_at < w.starts_at + make_interval(mins => w.duration_minutes)
        WHEN 'daily' THEN mod(floor(extract(epoch FROM p_at - w.starts_at))::bigint, 86400)  < w.duration_minutes * 60
        ELSE              mod(floor(extract(epoch FROM p_at - w.starts_at))::bigint, 604800) < w.duration_minutes * 60
    END
$$;

-- What a public status page shows as "Maintenance": scheduled windows only. A deploy quiets alerts for a few minutes; it is not an announcement.
-- (in_maintenance, which decides alerts and uptime, still counts deploys.)
CREATE FUNCTION in_announced_maintenance(p_org uuid, p_monitor uuid, p_at timestamptz) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM maintenance_windows w
        WHERE w.organization_id = p_org AND w.kind = 'scheduled' AND maintenance_window_active(w, p_at)
          AND (w.all_monitors OR EXISTS (SELECT 1 FROM maintenance_window_monitors wm WHERE wm.window_id = w.id AND wm.monitor_id = p_monitor)))
$$;

-- Deploy keys: an API key that can only start and finish deploys, safe to store in CI. OAuth grants stay read or write.
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scope_check,
    ADD CONSTRAINT api_tokens_scope_check CHECK (scope IN ('read', 'write', 'deploy'));
