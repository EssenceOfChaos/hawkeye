DROP TABLE incident_snapshots;
ALTER TABLE monitors DROP COLUMN capture_failure_body, DROP COLUMN last_good_fp, DROP COLUMN last_good;
