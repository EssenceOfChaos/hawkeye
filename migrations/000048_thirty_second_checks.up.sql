-- Agency accounts may check a website, API or DNS record every 30 seconds (docs/plan-sub-minute-checks.md). Heartbeats keep their 60-second
-- floor (a period is how often the customer's job runs, and grace times start at a minute), and so do multi-step API checks in the application,
-- whose steps together may take up to 60 seconds. New monitors still start at 60 seconds; 30 is something a person chooses.
--
-- NOT VALID: the new rule only widens the old one, so existing rows already satisfy it; validating would read the whole table under the lock.
ALTER TABLE monitors DROP CONSTRAINT monitors_check_interval_seconds_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_check_interval_seconds_check CHECK (check_interval_seconds BETWEEN
    CASE WHEN kind = 'heartbeat' THEN 60 ELSE 30 END AND CASE WHEN kind = 'heartbeat' THEN 2592000 ELSE 86400 END) NOT VALID;

UPDATE plans SET minimum_check_interval_seconds = 30 WHERE code = 'agency';
