DROP TABLE IF EXISTS check_jobs;
DROP TABLE IF EXISTS check_results;
DROP INDEX IF EXISTS monitors_due_idx;
ALTER TABLE monitors DROP COLUMN IF EXISTS next_check_at;
