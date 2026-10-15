DROP TABLE monitor_region_state;
DROP INDEX check_jobs_region_queued_idx;
-- Keep one job per monitor before restoring the old uniqueness.
DELETE FROM check_jobs a USING check_jobs b WHERE a.monitor_id = b.monitor_id AND a.id > b.id;
ALTER TABLE check_jobs DROP CONSTRAINT check_jobs_monitor_region_key;
ALTER TABLE check_jobs ADD CONSTRAINT check_jobs_monitor_id_key UNIQUE (monitor_id);
ALTER TABLE check_jobs DROP COLUMN region;
ALTER TABLE check_results DROP COLUMN region;
ALTER TABLE monitors DROP COLUMN regions;
ALTER TABLE plans DROP COLUMN max_regions;
