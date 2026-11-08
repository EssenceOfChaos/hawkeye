-- API keys: bearer tokens for the public API (and, later, OAuth access tokens for the MCP server; see docs/plan-api-mcp.md).
-- A key belongs to one organization and one person. Only a SHA-256 hash of the token is stored; the token itself is shown once, when the key is made.
-- The foreign key to the membership means a key goes the moment its owner leaves or is removed from the organization (and with the account or the
-- organization). The person's role is read again on every request, so a key never does more than its owner can do now.
CREATE TABLE api_tokens (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL,
    user_id             uuid NOT NULL,
    kind                text NOT NULL DEFAULT 'key' CHECK (kind IN ('key')),
    name                text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    scope               text NOT NULL CHECK (scope IN ('read', 'write')),
    prefix              text NOT NULL,                    -- the first characters of the token, so people can tell keys apart
    token_hash          bytea NOT NULL UNIQUE,
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_used_at        timestamptz,
    expires_at          timestamptz,                      -- null: never expires (the owner chose that)
    expiry_warned_at    timestamptz,                      -- the "expires in 7 days" email went out
    expired_notified_at timestamptz,                      -- the "has expired" email went out
    revoked_at          timestamptz,
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_members (organization_id, user_id) ON DELETE CASCADE,
    CHECK (expires_at IS NULL OR expires_at > created_at)
);
CREATE INDEX api_tokens_organization_idx ON api_tokens (organization_id) WHERE revoked_at IS NULL;
CREATE INDEX api_tokens_user_idx ON api_tokens (user_id);
CREATE INDEX api_tokens_expiry_idx ON api_tokens (expires_at) WHERE revoked_at IS NULL AND expires_at IS NOT NULL;

-- Read access through the API is on every plan; changing data through it is on paid plans.
ALTER TABLE plans ADD COLUMN api_write_enabled boolean NOT NULL DEFAULT false;
UPDATE plans SET api_write_enabled = true WHERE code IN ('pro', 'agency');
