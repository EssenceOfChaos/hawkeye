-- Heartbeat (cron job) monitors. HawkEye does not check anything for these: the customer's job requests the monitor's secret ping address
-- (/ping/<ping_token>) each time it runs, and the monitor is overdue once ping_deadline has passed. check_interval_seconds is the expected time between
-- pings (the period, up to 30 days for heartbeats) and heartbeat_grace_seconds the extra time allowed before a late ping counts as missed. ping_deadline
-- stays NULL until the first ping arrives, so a new heartbeat never alerts before the job has been set up. Heartbeats have no URL ('').
ALTER TABLE monitors
    ADD COLUMN kind text NOT NULL DEFAULT 'http' CHECK (kind IN ('http', 'heartbeat')),
    ADD COLUMN ping_token text UNIQUE CHECK (ping_token ~ '^[A-Za-z0-9_-]{22}$'),
    ADD COLUMN heartbeat_grace_seconds integer CHECK (heartbeat_grace_seconds BETWEEN 60 AND 604800),
    ADD COLUMN last_ping_at timestamptz,
    ADD COLUMN ping_deadline timestamptz,
    ADD CONSTRAINT monitors_heartbeat_fields CHECK (CASE WHEN kind = 'heartbeat' THEN heartbeat_grace_seconds IS NOT NULL
        ELSE ping_token IS NULL AND heartbeat_grace_seconds IS NULL AND last_ping_at IS NULL AND ping_deadline IS NULL END),
    DROP CONSTRAINT monitors_check_interval_seconds_check,
    ADD CONSTRAINT monitors_check_interval_seconds_check CHECK (check_interval_seconds BETWEEN 60 AND CASE WHEN kind = 'heartbeat' THEN 2592000 ELSE 86400 END);
CREATE INDEX monitors_ping_deadline_idx ON monitors (ping_deadline) WHERE kind = 'heartbeat' AND enabled AND deleted_at IS NULL;

-- A missed ping and a job that reported its own failure are recorded as failed results, so uptime, incidents and reports work as for any monitor.
ALTER TABLE check_results DROP CONSTRAINT check_results_error_category_check;
ALTER TABLE check_results ADD CONSTRAINT check_results_error_category_check CHECK (error_category IN
    ('dns', 'connect', 'tls', 'timeout', 'redirect', 'blocked', 'http_status', 'assertion', 'other', 'missed_ping', 'job_failed'));

-- The most recent pings of each heartbeat (internal/heartbeats keeps the last 100), shown in the app so customers can see their job reaching HawkEye.
-- body is the start of what the job sent (for example the end of its log), visible only to the organization.
CREATE TABLE heartbeat_pings (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    monitor_id  uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    received_at timestamptz NOT NULL DEFAULT now(),
    kind        text NOT NULL CHECK (kind IN ('success', 'fail')),
    exit_status smallint CHECK (exit_status BETWEEN 0 AND 255),
    method      text NOT NULL CHECK (char_length(method) <= 10),
    source_ip   text NOT NULL CHECK (char_length(source_ip) <= 64),
    user_agent  text CHECK (char_length(user_agent) <= 200),
    body        text CHECK (char_length(body) <= 1000),
    ignored     boolean NOT NULL DEFAULT false -- the monitor was paused: the ping was logged but changed nothing
);
CREATE INDEX heartbeat_pings_monitor_idx ON heartbeat_pings (monitor_id, received_at DESC, id DESC);

-- Whether HawkEye itself could receive pings. The API processes mark it every few seconds; when they come back after a gap, gap_ended_at records it.
-- A ping that could not reach HawkEye because HawkEye was down must never be reported to the customer as a missed job, so a deadline that fell in
-- (or after) such a gap is moved to a full period after the gap ended (internal/heartbeats/sweeper.go).
CREATE TABLE ping_intake (
    singleton    boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    seen_at      timestamptz NOT NULL,
    gap_ended_at timestamptz NOT NULL
);
INSERT INTO ping_intake (seen_at, gap_ended_at) VALUES (now(), now());
