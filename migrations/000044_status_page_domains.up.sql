-- Status pages on a customer's own domain (Agency; docs/plan-status-page-domains.md). The owner claims a subdomain such as status.clientbrand.com
-- for one page, adds a CNAME (routes visitors to us) and a TXT record holding this claim's token (proves they control the name), and the domain is
-- served once both are seen.
--
-- A host name may be claimed by several organizations while pending, so nobody can block the real owner by claiming their name first; only one
-- claim at a time can be verified (the partial unique index), and only a verified one is served. The token is not a secret: it is published in
-- the customer's own DNS, and knowing it does not help anyone who cannot write to that zone. Each claim gets a new one, so a TXT record left from an
-- old claim never verifies a new claimant.

-- The domain row repeats the page's organization so the per-organization limit and queries need no join; the composite key keeps them in step.
ALTER TABLE status_pages ADD CONSTRAINT status_pages_id_organization_key UNIQUE (id, organization_id);

CREATE TABLE status_page_domains (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id           uuid NOT NULL UNIQUE,
    organization_id   uuid NOT NULL,
    hostname          text NOT NULL CHECK (hostname ~ '^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$' AND char_length(hostname) BETWEEN 4 AND 253),
    verify_token      text NOT NULL CHECK (verify_token ~ '^[a-z2-7]{20,64}$'),
    -- pending: the records have not been seen yet. active: verified and served. failing: was active, a record is now wrong; still served while the
    -- owner fixes it. disabled: failed for too long, no longer served until it verifies again.
    state             text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'active', 'failing', 'disabled')),
    last_error        text CHECK (char_length(last_error) <= 300),
    last_checked_at   timestamptz,
    next_check_at     timestamptz,          -- NULL: not checked automatically (verification gave up, or disabled) until "Check now"
    verify_started_at timestamptz NOT NULL DEFAULT now(), -- start of the current attempt to verify (a pending claim gives up after a while)
    verified_at       timestamptz,
    failing_since     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (page_id, organization_id) REFERENCES status_pages (id, organization_id) ON DELETE CASCADE,
    CHECK ((state IN ('active', 'failing')) = (verified_at IS NOT NULL) OR state = 'disabled'),
    CHECK ((state = 'failing') = (failing_since IS NOT NULL))
);
CREATE UNIQUE INDEX status_page_domains_served_hostname_idx ON status_page_domains (hostname) WHERE state IN ('active', 'failing');
CREATE UNIQUE INDEX status_page_domains_org_hostname_idx ON status_page_domains (organization_id, hostname);
CREATE INDEX status_page_domains_due_idx ON status_page_domains (next_check_at) WHERE next_check_at IS NOT NULL;
CREATE TRIGGER status_page_domains_set_updated_at BEFORE UPDATE ON status_page_domains
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Off by default: /status/<slug> keeps working beside the domain unless the owner chooses to send visitors there.
ALTER TABLE status_pages ADD COLUMN redirect_to_domain boolean NOT NULL DEFAULT false;

ALTER TABLE plans ADD COLUMN max_status_page_domains integer NOT NULL DEFAULT 0 CHECK (max_status_page_domains >= 0);
UPDATE plans SET max_status_page_domains = 50 WHERE code = 'agency';
