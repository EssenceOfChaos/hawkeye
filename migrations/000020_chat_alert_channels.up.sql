-- Chat alert destinations: Discord webhooks, Microsoft Teams Workflows webhooks and Telegram.
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_channel_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_channel_check
    CHECK (channel IN ('email', 'webhook', 'slack', 'discord', 'teams', 'telegram'));

-- A Telegram chat reaches HawkEye in one of two ways. With the customer's own bot, `destination` is the sealed Bot API URL (its path carries the
-- bot token, its query the chat ID), as for the other URL channels. Through HawkEye's own bot (`hawkeye_bot`), `destination` is just the chat ID
-- in clear: it is not a credential without the bot token, which lives in configuration, and the bot's updates (removed from a chat, group upgraded)
-- must find the rows by it.
ALTER TABLE notification_settings ADD COLUMN hawkeye_bot boolean NOT NULL DEFAULT false;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_hawkeye_bot_check CHECK (NOT hawkeye_bot OR channel = 'telegram');
CREATE INDEX notification_settings_hawkeye_bot_chat_idx ON notification_settings (destination) WHERE hawkeye_bot;

-- One-time codes that connect a Telegram chat to an organization: the owner opens t.me/<bot>?start=<code>, and Telegram delivers "/start <code>"
-- from that chat to the bot's webhook. Only a hash is stored; a code is single use and short-lived.
CREATE TABLE telegram_connect_codes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    code_hash       bytea NOT NULL UNIQUE,
    expires_at      timestamptz NOT NULL,
    used_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX telegram_connect_codes_org_idx ON telegram_connect_codes (organization_id);
