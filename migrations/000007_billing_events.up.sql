-- When the subscription state was last read from the provider. (Webhook events are signals, not truth: the provider does not
-- guarantee ordering, so every subscription event triggers a re-read of the current state before it is applied.)
ALTER TABLE subscriptions ADD COLUMN last_synced_at timestamptz;

-- One provider customer belongs to exactly one organization.
CREATE UNIQUE INDEX subscriptions_provider_customer_key ON subscriptions (provider, provider_customer_id) WHERE provider_customer_id IS NOT NULL;

-- Every accepted webhook is stored durably before we answer 2xx; processing happens afterwards (inline once, then by
-- the worker with retries). UNIQUE(provider, provider_event_id) makes redelivery harmless.
CREATE TABLE billing_webhook_events (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider          text NOT NULL DEFAULT 'stripe',
    provider_event_id text NOT NULL,
    event_type        text NOT NULL,
    event_created_at  timestamptz NOT NULL,
    -- received: waiting (or waiting to be retried); processed; ignored (irrelevant, stale, or another subscription); failed: gave up
    status            text NOT NULL DEFAULT 'received' CHECK (status IN ('received', 'processed', 'ignored', 'failed')),
    payload           jsonb NOT NULL,
    received_at       timestamptz NOT NULL DEFAULT now(),
    processed_at      timestamptz,
    attempt_count     integer NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    last_error        text CHECK (char_length(last_error) <= 300),
    CONSTRAINT billing_webhook_events_unique UNIQUE (provider, provider_event_id)
);
CREATE INDEX billing_webhook_events_due_idx ON billing_webhook_events (event_created_at) WHERE status = 'received';
CREATE INDEX billing_webhook_events_received_at_idx ON billing_webhook_events (received_at);
