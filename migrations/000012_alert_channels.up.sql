-- Alert destinations beyond email: generic webhooks and Slack incoming webhooks.
-- For these channels `destination` holds the URL sealed with internal/secrets (the URL is a credential), `label`
-- is a masked form that is safe to show, `fingerprint` (SHA-256 of the URL) detects duplicates without decrypting,
-- and `signing_secret` (webhook only, sealed) signs each request.
ALTER TABLE notification_settings
    ADD COLUMN label          text CHECK (char_length(label) <= 200),
    ADD COLUMN fingerprint    text,
    ADD COLUMN signing_secret text;

ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_channel_check;
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_destination_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_channel_check CHECK (channel IN ('email', 'webhook', 'slack'));
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_destination_check CHECK (
    (channel = 'email' AND destination = lower(btrim(destination)) AND char_length(destination) <= 254)
    OR (channel <> 'email' AND fingerprint IS NOT NULL AND label IS NOT NULL AND char_length(destination) <= 4096));

CREATE UNIQUE INDEX notification_settings_unique_fingerprint
    ON notification_settings (organization_id, channel, fingerprint) WHERE fingerprint IS NOT NULL;
