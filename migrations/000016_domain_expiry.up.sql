-- Domain registration expiry monitoring. An organization tracks registrable domains (example.com, not www.example.com). Most are derived from the
-- hosts of its monitors; owners can also add domains by hand. A daily job looks each one up (RDAP, falling back to WHOIS) and warns at 30, 14, 7 and
-- 1 day(s) before the registration expires and once more when it has expired (bucket 0), like certificate warnings.
ALTER TABLE plans ADD COLUMN max_domains integer NOT NULL DEFAULT 3 CHECK (max_domains >= 0);
UPDATE plans SET max_domains = 25 WHERE code = 'pro';
UPDATE plans SET max_domains = 100 WHERE code = 'agency';

CREATE TABLE domains (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name               text NOT NULL CHECK (char_length(name) BETWEEN 3 AND 253 AND name = lower(name)),
    -- Added by hand. A domain that is not manual disappears when no monitor uses it any more.
    manual             boolean NOT NULL DEFAULT false,
    -- pending: not looked up yet. known: expires_at is set. unavailable: the registry does not publish an expiry date (unsupported TLD,
    -- redacted record, unregistered name); unavailable_reason says which. A lookup that merely failed (timeout, rate limit) changes none of this.
    status             text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'known', 'unavailable')),
    expires_at         timestamptz,
    source             text CHECK (source IN ('rdap', 'whois')),
    unavailable_reason text CHECK (char_length(unavailable_reason) <= 200),
    checked_at         timestamptz,                       -- last lookup that gave an answer (known or unavailable)
    next_check_at      timestamptz NOT NULL DEFAULT now(),
    failure_count      integer NOT NULL DEFAULT 0 CHECK (failure_count >= 0), -- consecutive failed lookups
    last_error         text CHECK (char_length(last_error) <= 200),
    -- The smallest warning bucket already queued for the registration that expires at notified_for; a renewal (new expiry date) resets it.
    notified_bucket    integer CHECK (notified_bucket IN (0, 1, 7, 14, 30)),
    notified_for       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name),
    CHECK ((status = 'known') = (expires_at IS NOT NULL))
);
CREATE INDEX domains_due_idx ON domains (next_check_at);

-- A domain warning belongs to a domain, not to an incident or a monitor.
ALTER TABLE notification_deliveries
    ADD COLUMN domain_id         uuid REFERENCES domains (id) ON DELETE CASCADE,
    ADD COLUMN domain_expires_at timestamptz,
    ADD COLUMN domain_bucket     integer;

ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_event_type_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_event_type_check
    CHECK (event_type IN ('outage', 'recovery', 'ssl_expiry', 'domain_expiry'));
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_shape;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_shape CHECK (
    (event_type = 'ssl_expiry' AND incident_id IS NULL AND monitor_id IS NOT NULL AND ssl_expires_at IS NOT NULL AND ssl_bucket IS NOT NULL
        AND domain_id IS NULL)
    OR (event_type = 'domain_expiry' AND incident_id IS NULL AND monitor_id IS NULL AND domain_id IS NOT NULL
        AND domain_expires_at IS NOT NULL AND domain_bucket IS NOT NULL)
    OR (event_type IN ('outage', 'recovery') AND incident_id IS NOT NULL AND domain_id IS NULL));

-- One domain warning per domain, recipient, registration (its expiry date) and bucket.
CREATE UNIQUE INDEX notification_deliveries_domain_idem
    ON notification_deliveries (domain_id, notification_setting_id, domain_expires_at, domain_bucket) WHERE event_type = 'domain_expiry';
