-- Two-factor sign-in with an authenticator app (TOTP, RFC 6238), optional for each person; owners may require it for their organization.
-- The secret is sealed with SECRETS_ENCRYPTION_KEY (internal/secrets), bound to the user. totp_last_step is the last 30-second step accepted, so a
-- code can't be used twice. A secret being set up is kept apart (totp_pending_secret) until the person proves their app works.
ALTER TABLE users
    ADD COLUMN totp_secret         text,
    ADD COLUMN totp_pending_secret text,
    ADD COLUMN totp_enabled_at     timestamptz,
    ADD COLUMN totp_last_step      bigint,
    ADD CONSTRAINT users_totp_consistent CHECK ((totp_secret IS NULL) = (totp_enabled_at IS NULL));

-- Single-use recovery codes, for when the authenticator app is lost. Only a SHA-256 hash of each (80 random bits) is stored.
CREATE TABLE recovery_codes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at    timestamptz
);
CREATE INDEX recovery_codes_user_idx ON recovery_codes (user_id) WHERE used_at IS NULL;

-- The step between a correct password and the code: a short-lived, hashed token that is the only thing the browser holds in between. A few wrong
-- codes use it up, and it never becomes a session by itself.
CREATE TABLE login_challenges (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    attempts   integer NOT NULL DEFAULT 0
);
CREATE INDEX login_challenges_expiry_idx ON login_challenges (expires_at);

-- An owner can require two-factor sign-in for everyone in their organization. Off (null) by default. Each member without it has 7 days from the
-- later of this time and when they joined before the organization is closed to them (and to their API keys) until they set it up.
ALTER TABLE organizations ADD COLUMN require_two_factor_since timestamptz;
