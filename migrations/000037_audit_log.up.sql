-- The security log (internal/audit): who did what, when, from where, and with which credential (a session, an API key or a connected app).
--
-- An event belongs to exactly one log. Organization events (monitors, keys, team, alert recipients...) are read by the organization's owner and go
-- with the organization. Account events (sign-ins, password, two-factor...) are read by that person and go with their account. The person who acted
-- is copied (actor_user_id, actor_email) rather than referenced, so an organization keeps its record of what a former member did; it is not a foreign
-- key, so nothing ever has to rewrite a row.
--
-- Rows are never changed: the trigger below refuses UPDATE. They are removed only by retention after 365 days (internal/retention) or with their
-- organization or account.
CREATE TABLE audit_events (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at     timestamptz NOT NULL DEFAULT now(),
    organization_id uuid REFERENCES organizations (id) ON DELETE CASCADE,
    account_id      uuid REFERENCES users (id) ON DELETE CASCADE,
    action          text NOT NULL CHECK (action ~ '^[a-z_]+\.[a-z_]+$'),
    actor_type      text NOT NULL CHECK (actor_type IN ('user', 'api_key', 'oauth_app', 'anonymous', 'system')),
    actor_user_id   uuid,
    actor_email     text CHECK (char_length(actor_email) <= 254),
    credential_id   uuid,
    credential_name text CHECK (char_length(credential_name) <= 200),
    target_type     text CHECK (char_length(target_type) <= 40),
    target_id       text CHECK (char_length(target_id) <= 100),
    target_name     text CHECK (char_length(target_name) <= 300),
    details         jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(details) = 'object'),
    ip              inet,
    user_agent      text CHECK (char_length(user_agent) <= 300),
    request_id      text CHECK (char_length(request_id) <= 100),
    CHECK ((organization_id IS NULL) <> (account_id IS NULL))
);
CREATE INDEX audit_events_organization_idx ON audit_events (organization_id, id DESC) WHERE organization_id IS NOT NULL;
CREATE INDEX audit_events_account_idx ON audit_events (account_id, id DESC) WHERE account_id IS NOT NULL;
CREATE INDEX audit_events_occurred_idx ON audit_events (occurred_at);

CREATE FUNCTION audit_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only';
END
$$;
CREATE TRIGGER audit_events_no_update BEFORE UPDATE ON audit_events FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();
