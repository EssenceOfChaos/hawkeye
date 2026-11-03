DROP TABLE ping_intake;
DROP TABLE heartbeat_pings;
-- Heartbeat monitors cannot exist without these columns; their history goes with them.
DELETE FROM monitors WHERE kind = 'heartbeat';
DELETE FROM check_results WHERE error_category IN ('missed_ping', 'job_failed');
ALTER TABLE check_results DROP CONSTRAINT check_results_error_category_check;
ALTER TABLE check_results ADD CONSTRAINT check_results_error_category_check CHECK (error_category IN
    ('dns', 'connect', 'tls', 'timeout', 'redirect', 'blocked', 'http_status', 'assertion', 'other'));
DROP INDEX monitors_ping_deadline_idx;
ALTER TABLE monitors DROP CONSTRAINT monitors_check_interval_seconds_check,
    ADD CONSTRAINT monitors_check_interval_seconds_check CHECK (check_interval_seconds BETWEEN 60 AND 86400),
    DROP CONSTRAINT monitors_heartbeat_fields,
    DROP COLUMN ping_deadline, DROP COLUMN last_ping_at, DROP COLUMN heartbeat_grace_seconds, DROP COLUMN ping_token, DROP COLUMN kind;
