DELETE FROM notification_settings WHERE channel <> 'email';
DROP INDEX notification_settings_unique_fingerprint;
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_destination_check;
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_channel_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_channel_check CHECK (channel IN ('email'));
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_destination_check CHECK (destination = lower(btrim(destination)) AND char_length(destination) <= 254);
ALTER TABLE notification_settings DROP COLUMN signing_secret, DROP COLUMN fingerprint, DROP COLUMN label;
