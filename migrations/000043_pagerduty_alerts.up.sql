-- PagerDuty alert destinations (Events API v2). `destination` is the sealed enqueue URL for the service's region with the integration key in its
-- query (moved into the body at send time), like the other URL channels.
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_channel_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_channel_check
    CHECK (channel IN ('email', 'webhook', 'slack', 'discord', 'teams', 'telegram', 'pagerduty'));
