-- Sign in with Google and GitHub (docs/plan-social-sign-in.md).

-- An account created with Google or GitHub has no password until its owner sets one.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- A Google or GitHub account that signs in to a HawkEye account, matched by the provider's ID (never by email). The email is kept as the
-- provider gave it, to show in settings; the account's own address never changes because of it.
CREATE TABLE user_identities (
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider     text NOT NULL CHECK (provider IN ('google', 'github')),
    subject      text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 255),
    email        text NOT NULL CHECK (char_length(email) <= 320),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject),
    UNIQUE (user_id, provider)
);

-- One sign-in in progress: the random state sent to the provider (hashed), what to do on return, and the PKCE verifier and nonce. Single use,
-- 10 minutes. The browser holds the state in a cookie, so a callback only works in the browser that started it.
CREATE TABLE social_auth_states (
    state_hash    bytea PRIMARY KEY,
    provider      text NOT NULL,
    intent        text NOT NULL CHECK (intent IN ('signin', 'connect', 'confirm')),
    user_id       uuid REFERENCES users (id) ON DELETE CASCADE, -- connect and confirm: the signed-in person
    pkce_verifier text NOT NULL,
    nonce         text NOT NULL,
    next_path     text NOT NULL DEFAULT '',
    attribution   jsonb, -- how a new visitor found HawkEye, kept for an account created on return
    expires_at    timestamptz NOT NULL
);
CREATE INDEX social_auth_states_expiry_idx ON social_auth_states (expires_at);

-- When the session's person last proved it's them for a sensitive action without a password (signing in with their provider again).
ALTER TABLE sessions ADD COLUMN confirmed_at timestamptz;

-- How the first step of a two-factor sign-in was passed, for the security log.
ALTER TABLE login_challenges ADD COLUMN first_factor text NOT NULL DEFAULT 'password'
    CHECK (first_factor IN ('password', 'google', 'github'));
