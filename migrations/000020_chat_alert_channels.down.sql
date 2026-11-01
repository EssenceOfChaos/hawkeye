DROP TABLE telegram_connect_codes;
DELETE FROM notification_settings WHERE channel IN ('discord', 'teams', 'telegram');
DROP INDEX notification_settings_hawkeye_bot_chat_idx;
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_hawkeye_bot_check;
ALTER TABLE notification_settings DROP COLUMN hawkeye_bot;
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_channel_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_channel_check CHECK (channel IN ('email', 'webhook', 'slack'));
