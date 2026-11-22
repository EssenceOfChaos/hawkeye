DELETE FROM api_tokens WHERE scope = 'deploy';
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scope_check,
    ADD CONSTRAINT api_tokens_scope_check CHECK (scope IN ('read', 'write'));
DROP FUNCTION in_announced_maintenance(uuid, uuid, timestamptz);
DELETE FROM maintenance_windows WHERE kind = 'deploy';
CREATE OR REPLACE FUNCTION maintenance_window_active(w maintenance_windows, p_at timestamptz) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT p_at >= w.starts_at AND CASE w.repeat
        WHEN 'none'  THEN p_at < w.starts_at + make_interval(mins => w.duration_minutes)
        WHEN 'daily' THEN mod(floor(extract(epoch FROM p_at - w.starts_at))::bigint, 86400)  < w.duration_minutes * 60
        ELSE              mod(floor(extract(epoch FROM p_at - w.starts_at))::bigint, 604800) < w.duration_minutes * 60
    END
$$;
DROP INDEX maintenance_windows_deploys_idx;
ALTER TABLE maintenance_windows DROP CONSTRAINT maintenance_windows_ended_only_deploys,
    DROP COLUMN verified, DROP COLUMN started_by_token, DROP COLUMN target, DROP COLUMN note, DROP COLUMN ended_at, DROP COLUMN kind;
