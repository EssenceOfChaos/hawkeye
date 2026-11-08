-- OAuth 2.1 for the MCP server (docs/plan-api-mcp.md, Phase 3b): Claude and other MCP clients connect to someone's HawkEye organization without an API
-- key being copied by hand. Only the parts MCP clients need: authorization code with PKCE, rotating refresh tokens, revocation.

-- Registered clients (Claude, added with cmd/oauthclient). A client with a secret authenticates at the token endpoint; one without is public and relies
-- on PKCE alone. Redirect URIs must match exactly.
CREATE TABLE oauth_clients (
    id            text PRIMARY KEY,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    secret_hash   bytea,
    redirect_uris text[] NOT NULL CHECK (cardinality(redirect_uris) BETWEEN 1 AND 10),
    created_at    timestamptz NOT NULL DEFAULT now(),
    disabled_at   timestamptz
);

-- A grant is one connection a person made: this client may act as them in this organization with this scope. It goes with the membership, like an
-- API key, and revoking it ends every token issued under it.
CREATE TABLE oauth_grants (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id       text NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    organization_id uuid NOT NULL,
    user_id         uuid NOT NULL,
    scope           text NOT NULL CHECK (scope IN ('read', 'write')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_members (organization_id, user_id) ON DELETE CASCADE
);
CREATE INDEX oauth_grants_organization_idx ON oauth_grants (organization_id) WHERE revoked_at IS NULL;

-- Authorization codes: single use, a minute long, bound to the client, the redirect URI and the PKCE challenge. Hashed. grant_id is set when the code is
-- exchanged, so a second use can revoke what the first one issued.
CREATE TABLE oauth_codes (
    code_hash       bytea PRIMARY KEY,
    client_id       text NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    organization_id uuid NOT NULL,
    user_id         uuid NOT NULL,
    scope           text NOT NULL CHECK (scope IN ('read', 'write')),
    redirect_uri    text NOT NULL,
    code_challenge  text NOT NULL,
    resource        text,
    expires_at      timestamptz NOT NULL,
    used_at         timestamptz,
    grant_id        uuid REFERENCES oauth_grants (id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_members (organization_id, user_id) ON DELETE CASCADE
);
CREATE INDEX oauth_codes_expiry_idx ON oauth_codes (expires_at);

-- Refresh tokens rotate: each use replaces the token. Presenting one that was already used means it leaked, so the whole grant is revoked.
CREATE TABLE oauth_refresh_tokens (
    token_hash bytea PRIMARY KEY,
    grant_id   uuid NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);
CREATE INDEX oauth_refresh_grant_idx ON oauth_refresh_tokens (grant_id);

-- Access tokens are API tokens of their own kind: short-lived, accepted only by the MCP server, and tied to their grant.
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_kind_check;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_kind_check CHECK (kind IN ('key', 'oauth_access')),
    ADD COLUMN oauth_grant_id uuid REFERENCES oauth_grants (id) ON DELETE CASCADE,
    ADD CONSTRAINT api_tokens_oauth_grant CHECK ((kind = 'oauth_access') = (oauth_grant_id IS NOT NULL));
