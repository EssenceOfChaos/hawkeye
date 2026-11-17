-- Hourly totals of check results (internal/rollups), so uptime, status pages, charts and reports add up hours instead of every result: at 60-second
-- checks a 90-day status page for 50 monitors read 6.5 million rows (docs/plan-sub-minute-checks.md).
--
-- A row is one monitor's results for one UTC hour, with those recorded during maintenance kept apart (uptime leaves them out, charts don't). Results
-- are never changed once recorded, so a closed hour's totals stay true. rollup_state.through says up to when the totals are complete (NULL: not yet
-- built); readers use rollups before it and raw results after it, so their answers are the same either way, only faster.
CREATE TABLE check_rollups (
    monitor_id     uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    hour           timestamptz NOT NULL CHECK (hour = date_trunc('hour', hour AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    in_maintenance boolean NOT NULL,
    checks         integer NOT NULL CHECK (checks > 0),
    successes      integer NOT NULL CHECK (successes BETWEEN 0 AND checks),
    -- Response times of the successful checks that have one: their sum and count (for averages) and the largest.
    ms_sum         bigint NOT NULL CHECK (ms_sum >= 0),
    ms_count       integer NOT NULL CHECK (ms_count BETWEEN 0 AND successes),
    ms_max         integer CHECK (ms_max >= 0),
    PRIMARY KEY (monitor_id, hour, in_maintenance)
);
-- The rollup job replaces whole hours across every monitor.
CREATE INDEX check_rollups_hour_idx ON check_rollups (hour);

CREATE TABLE rollup_state (
    id      boolean PRIMARY KEY DEFAULT true CHECK (id),
    through timestamptz CHECK (through = date_trunc('hour', through AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
    -- The highest check_results id the job has looked at. A result recorded later than usual for an hour already added up (a heartbeat's missed
    -- deadline, recorded when the worker comes back) has a higher id, so the job finds it and adds that monitor's hour up again.
    seen_id bigint
);
INSERT INTO rollup_state DEFAULT VALUES;
