ALTER TABLE notification_deliveries DROP COLUMN suppressed;
ALTER TABLE monitors DROP CONSTRAINT monitors_quiet_hours_consistent,
    DROP COLUMN quiet_timezone, DROP COLUMN quiet_end, DROP COLUMN quiet_start, DROP COLUMN alert_delay_minutes;
DROP TABLE notification_setting_tags;
ALTER TABLE notification_settings DROP COLUMN monitor_scope;
