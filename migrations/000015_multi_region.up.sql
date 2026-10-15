-- Multi-region checks. A monitor may run from several regions (chosen from the installation's configured list, up to the plan's
-- limit). Each region keeps its own streak; the monitor counts as down when a majority of its regions are down.
ALTER TABLE plans ADD COLUMN max_regions integer NOT NULL DEFAULT 1 CHECK (max_regions >= 1);
UPDATE plans SET max_regions = 3 WHERE code = 'pro';
UPDATE plans SET max_regions = 5 WHERE code = 'agency';

-- Empty means "the primary region only".
ALTER TABLE monitors ADD COLUMN regions text[] NOT NULL DEFAULT '{}';
ALTER TABLE check_results ADD COLUMN region text NOT NULL DEFAULT 'primary';

-- One outstanding job per monitor *and region*; workers claim only the jobs of their own region.
ALTER TABLE check_jobs ADD COLUMN region text NOT NULL DEFAULT 'primary';
ALTER TABLE check_jobs DROP CONSTRAINT check_jobs_monitor_id_key;
ALTER TABLE check_jobs ADD CONSTRAINT check_jobs_monitor_region_key UNIQUE (monitor_id, region);
CREATE INDEX check_jobs_region_queued_idx ON check_jobs (region, run_at) WHERE status = 'queued';

CREATE TABLE monitor_region_state (
    monitor_id            uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    region                text NOT NULL,
    status                text NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'up', 'down')),
    consecutive_failures  integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    consecutive_successes integer NOT NULL DEFAULT 0 CHECK (consecutive_successes >= 0),
    last_checked_at       timestamptz,
    last_error            text CHECK (char_length(last_error) <= 200),
    PRIMARY KEY (monitor_id, region)
);
