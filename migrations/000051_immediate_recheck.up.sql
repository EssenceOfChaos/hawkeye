-- Immediate re-check before alerting (docs/plan-immediate-recheck.md). When a healthy monitor fails, the same location checks it again shortly
-- after, instead of waiting a whole interval for the confirming check.
--
-- monitors.recheck: 'auto' re-checks only when every request is read-only (GET, HEAD, OPTIONS; DNS lookups), so a request that may change
-- something is never sent again unasked; 'always' re-checks whatever the methods; 'off' never does.
-- check_jobs.recheck: the queued job is a re-check (the job that failed, put back to run again soon).
-- check_results.recheck: the result came from a re-check. Kept in the history, left out of uptime, response times and rollups.
--
-- Constant defaults: metadata-only changes, so check_results is not rewritten.
ALTER TABLE monitors ADD COLUMN recheck text NOT NULL DEFAULT 'auto' CHECK (recheck IN ('auto', 'always', 'off'));
ALTER TABLE check_jobs ADD COLUMN recheck boolean NOT NULL DEFAULT false;
ALTER TABLE check_results ADD COLUMN recheck boolean NOT NULL DEFAULT false;
