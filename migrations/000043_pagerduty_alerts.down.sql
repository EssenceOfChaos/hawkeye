DELETE FROM notification_settings WHERE channel = 'pagerduty';
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_channel_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_channel_check
    CHECK (channel IN ('email', 'webhook', 'slack', 'discord', 'teams', 'telegram'));
