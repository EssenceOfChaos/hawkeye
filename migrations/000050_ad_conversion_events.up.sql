-- Conversions reported to OpenAI Ads by our server (its Conversions API), next to the browser pixel, which ad blockers and closed tabs can stop.
-- Written only when the visitor had analytics on (no opt-out cookie, no Global Privacy Control) and the server has an OPENAI_ADS_API_KEY, then sent by
-- the worker (internal/adconversions). event_id is the same value the pixel sends for that event, so OpenAI counts the conversion once.
-- oppref and obref are copies of OpenAI's own first-party cookies (__oppref: the ad click, __obref: a random browser reference). No email, name or
-- IP address is kept or sent. A purchase copies the context of the checkout that led to it, because Stripe's webhook has no browser behind it.
-- Rows are deleted with the account or organization, and 30 days after they were recorded.
CREATE TABLE ad_conversion_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type      text NOT NULL CHECK (event_type IN ('registration_completed', 'checkout_started', 'subscription_created')),
    event_id        text NOT NULL CHECK (char_length(event_id) BETWEEN 1 AND 100),
    user_id         uuid REFERENCES users (id) ON DELETE CASCADE,
    organization_id uuid REFERENCES organizations (id) ON DELETE CASCADE,
    occurred_at     timestamptz NOT NULL,
    source_path     text NOT NULL CHECK (source_path LIKE '/%' AND char_length(source_path) <= 200),
    data            jsonb NOT NULL,
    oppref          text CHECK (char_length(oppref) <= 512),
    obref           text CHECK (char_length(obref) <= 512),
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'expired')),
    attempt_count   integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    sent_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_type, event_id)
);

CREATE INDEX ad_conversion_events_due ON ad_conversion_events (next_attempt_at) WHERE status = 'pending';
CREATE INDEX ad_conversion_events_checkout ON ad_conversion_events (organization_id, occurred_at DESC) WHERE event_type = 'checkout_started';
CREATE INDEX ad_conversion_events_user ON ad_conversion_events (user_id);
CREATE INDEX ad_conversion_events_created_at ON ad_conversion_events (created_at);
