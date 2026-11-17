UPDATE plans SET minimum_check_interval_seconds = 60 WHERE code = 'agency';
UPDATE monitors SET check_interval_seconds = 60 WHERE check_interval_seconds < 60;

ALTER TABLE monitors DROP CONSTRAINT monitors_check_interval_seconds_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_check_interval_seconds_check CHECK (check_interval_seconds BETWEEN
    60 AND CASE WHEN kind = 'heartbeat' THEN 2592000 ELSE 86400 END);
