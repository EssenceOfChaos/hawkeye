-- Alert routing: a destination receives alerts for all of the organization's monitors (the default, as before) or only for monitors carrying one of its
-- tags. An alert that no destination would receive falls back to the team members' own email addresses (internal/notifications/routing.go).
ALTER TABLE notification_settings ADD COLUMN monitor_scope text NOT NULL DEFAULT 'all' CHECK (monitor_scope IN ('all', 'tags'));

-- Both sides belong to the same organization; the application checks this before linking, like monitor_tags.
CREATE TABLE notification_setting_tags (
    setting_id uuid NOT NULL REFERENCES notification_settings (id) ON DELETE CASCADE,
    tag_id     uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (setting_id, tag_id)
);
CREATE INDEX notification_setting_tags_tag_idx ON notification_setting_tags (tag_id);

-- Per monitor: alert only once it has been down this many minutes (0: as soon as the outage is confirmed), and quiet hours, a daily local-time window
-- (minutes after midnight, in quiet_timezone; it may cross midnight) during which its alerts are held until the window ends.
ALTER TABLE monitors
    ADD COLUMN alert_delay_minutes integer NOT NULL DEFAULT 0 CHECK (alert_delay_minutes BETWEEN 0 AND 1440),
    ADD COLUMN quiet_start smallint CHECK (quiet_start BETWEEN 0 AND 1439),
    ADD COLUMN quiet_end   smallint CHECK (quiet_end BETWEEN 0 AND 1439),
    ADD COLUMN quiet_timezone text CHECK (char_length(quiet_timezone) BETWEEN 1 AND 64),
    ADD CONSTRAINT monitors_quiet_hours_consistent CHECK ((quiet_start IS NULL) = (quiet_end IS NULL) AND (quiet_start IS NULL) = (quiet_timezone IS NULL)
        AND (quiet_start IS NULL OR quiet_start <> quiet_end));

-- A held alert (delayed or in quiet hours) whose problem ended before it was due is skipped with this reason, and the matching "recovered" or
-- "back to normal" alert is then not sent to that destination either: it never heard about the problem.
ALTER TABLE notification_deliveries ADD COLUMN suppressed text CHECK (suppressed IN ('ended_while_held'));
