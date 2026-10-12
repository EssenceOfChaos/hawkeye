CREATE TABLE users (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email             text NOT NULL,
    password_hash     text NOT NULL,
    email_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    disabled_at       timestamptz,
    -- Emails are normalised (trimmed, lower-cased) by the application; the constraint enforces it.
    CONSTRAINT users_email_normalized CHECK (email = lower(btrim(email))),
    CONSTRAINT users_email_key UNIQUE (email)
);
CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE organizations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER organizations_set_updated_at BEFORE UPDATE ON organizations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE organization_members (
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('owner', 'member')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);
CREATE INDEX organization_members_user_id_idx ON organization_members (user_id);

CREATE TABLE plans (
    id                             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                           text NOT NULL UNIQUE,
    name                           text NOT NULL,
    monthly_price_cents            integer NOT NULL CHECK (monthly_price_cents >= 0),
    max_monitors                   integer NOT NULL CHECK (max_monitors >= 0),
    minimum_check_interval_seconds integer NOT NULL CHECK (minimum_check_interval_seconds > 0),
    history_retention_days         integer NOT NULL CHECK (history_retention_days > 0),
    max_team_members               integer NOT NULL CHECK (max_team_members > 0),
    reports_enabled                boolean NOT NULL DEFAULT false,
    branded_reports_enabled        boolean NOT NULL DEFAULT false,
    active                         boolean NOT NULL DEFAULT true
);
-- Starting values; configurable data, not code. Stripe price IDs come from configuration.
INSERT INTO plans (code, name, monthly_price_cents, max_monitors, minimum_check_interval_seconds,
                   history_retention_days, max_team_members, reports_enabled, branded_reports_enabled) VALUES
    ('free',   'Free',     0,  5, 300,   7, 1, false, false),
    ('pro',    'Pro',   1900, 50,  60,  90, 1, true,  false),
    ('agency', 'Agency',5900,200,  60, 365, 5, true,  true);

-- Only a SHA-256 hash of each token is stored; a database leak does not expose usable tokens.
CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE password_reset_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);

CREATE TABLE email_verification_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_verification_tokens_user_id_idx ON email_verification_tokens (user_id);
