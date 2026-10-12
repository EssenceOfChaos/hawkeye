CREATE TABLE subscriptions (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id          uuid NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    plan_id                  uuid NOT NULL REFERENCES plans (id),
    provider                 text NOT NULL DEFAULT 'none' CHECK (provider IN ('none', 'stripe')),
    provider_customer_id     text,
    provider_subscription_id text,
    status                   text NOT NULL CHECK (status IN ('active', 'trialing', 'past_due', 'canceled', 'incomplete')),
    current_period_start     timestamptz,
    current_period_end       timestamptz,
    cancel_at_period_end     boolean NOT NULL DEFAULT false,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX subscriptions_provider_subscription_id_key
    ON subscriptions (provider, provider_subscription_id) WHERE provider_subscription_id IS NOT NULL;
CREATE TRIGGER subscriptions_set_updated_at BEFORE UPDATE ON subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Every existing organization starts on the free plan.
INSERT INTO subscriptions (organization_id, plan_id, status)
SELECT o.id, p.id, 'active' FROM organizations o CROSS JOIN plans p WHERE p.code = 'free';

CREATE TABLE monitors (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id        uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name                   text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    url                    text NOT NULL CHECK (char_length(url) <= 2048),
    method                 text NOT NULL DEFAULT 'GET' CHECK (method IN ('GET', 'HEAD')),
    expected_status_code   smallint CHECK (expected_status_code BETWEEN 100 AND 599),
    check_interval_seconds integer NOT NULL CHECK (check_interval_seconds BETWEEN 60 AND 86400),
    timeout_seconds        integer NOT NULL DEFAULT 10 CHECK (timeout_seconds BETWEEN 1 AND 30),
    enabled                boolean NOT NULL DEFAULT true,
    current_status         text NOT NULL DEFAULT 'unknown' CHECK (current_status IN ('unknown', 'up', 'down')),
    last_checked_at        timestamptz,
    last_success_at        timestamptz,
    consecutive_failures   integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    consecutive_successes  integer NOT NULL DEFAULT 0 CHECK (consecutive_successes >= 0),
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    deleted_at             timestamptz
);
-- Soft-deleted monitors keep their history until retention removes it; they do not count toward limits.
CREATE INDEX monitors_org_created_idx ON monitors (organization_id, created_at DESC, id) WHERE deleted_at IS NULL;
CREATE TRIGGER monitors_set_updated_at BEFORE UPDATE ON monitors
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
