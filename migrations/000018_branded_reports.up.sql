-- Monthly PDF uptime reports. Pro and Agency can download one per website; Agency puts its own name and logo on them. An organization can also have
-- the previous month's reports emailed to its owners on the 1st, so they can forward each PDF to the client it belongs to.
CREATE TABLE organization_branding (
    organization_id      uuid PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    -- Shown at the top of branded reports instead of "HawkEye". Plain text, one line.
    display_name         text NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 100),
    -- The logo, already validated and re-encoded by the application (PNG or JPEG, small), so a stored file is always safe to draw into a PDF.
    logo                 bytea CHECK (logo IS NULL OR octet_length(logo) <= 524288),
    logo_type            text CHECK (logo_type IN ('png', 'jpeg')),
    logo_width           integer CHECK (logo_width > 0),
    logo_height          integer CHECK (logo_height > 0),
    monthly_report_email boolean NOT NULL DEFAULT false,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CHECK ((logo IS NULL) = (logo_type IS NULL) AND (logo IS NULL) = (logo_width IS NULL) AND (logo IS NULL) = (logo_height IS NULL))
);
CREATE TRIGGER organization_branding_set_updated_at BEFORE UPDATE ON organization_branding
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- What the monthly job has already sent. One row per owner and email (a large account gets several emails, "part" 1, 2, ...), so a retry after
-- a crash or a temporary mail failure never sends anyone the same email twice. Rows older than a few months are of no use and cost nothing.
CREATE TABLE monthly_report_emails (
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    month           date        NOT NULL CHECK (extract(day FROM month) = 1), -- the first day of the month that was reported
    part            integer     NOT NULL CHECK (part >= 1),
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    sent_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, month, part, user_id)
);

-- Marks an organization's month as finished, so later passes skip it with one lookup.
CREATE TABLE monthly_report_runs (
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    month           date        NOT NULL CHECK (extract(day FROM month) = 1),
    completed_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, month)
);
