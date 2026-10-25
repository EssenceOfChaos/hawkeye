-- Onboarding ("lifecycle") emails: friendly nudges to people who signed up but have not started monitoring. What a person should receive is worked out
-- from their data each time (see internal/lifecycle), so there is no funnel-stage column to keep in step. This migration only records what was sent,
-- so no email is ever sent twice, and whether the person asked to stop.
CREATE TABLE lifecycle_emails (
    user_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind    text        NOT NULL CHECK (kind IN ('no_monitor_1d', 'no_monitor_3d', 'no_monitor_7d')),
    sent_at timestamptz NOT NULL DEFAULT now(),
    -- One row per person and email: the claim that makes a send happen at most once, even with several workers.
    PRIMARY KEY (user_id, kind)
);
CREATE INDEX lifecycle_emails_sent_at_idx ON lifecycle_emails (user_id, sent_at DESC);

-- opt_out: the person used the unsubscribe link. It stops lifecycle email only; alerts, verification and security email are unaffected.
-- unsubscribe_token: an unguessable key in the unsubscribe link (the person need not be signed in). It can only unsubscribe, so it is stored as is
-- because every email has to carry it again.
ALTER TABLE users
    ADD COLUMN lifecycle_opt_out_at timestamptz,
    ADD COLUMN unsubscribe_token    uuid NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX users_unsubscribe_token_key ON users (unsubscribe_token);

-- The job looks only at recently verified, still-subscribed people.
CREATE INDEX users_lifecycle_candidates_idx ON users (email_verified_at)
    WHERE email_verified_at IS NOT NULL AND lifecycle_opt_out_at IS NULL AND disabled_at IS NULL;
