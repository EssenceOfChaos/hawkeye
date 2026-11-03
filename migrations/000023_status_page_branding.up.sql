-- Branding per status page, so each client's page can carry that client's look: a logo, an accent color and a link back to the client's own website
-- (Pro and Agency), and on Agency the option to hide "Powered by HawkEye". Stored values are kept after a downgrade but only shown while the plan
-- includes them.
ALTER TABLE status_pages
    -- The logo, validated and re-encoded by the application (PNG or JPEG, small), like report logos. Only ever served as an image.
    ADD COLUMN logo            bytea CHECK (logo IS NULL OR octet_length(logo) <= 524288),
    ADD COLUMN logo_type       text CHECK (logo_type IN ('png', 'jpeg')),
    ADD COLUMN logo_width      integer CHECK (logo_width > 0),
    ADD COLUMN logo_height     integer CHECK (logo_height > 0),
    ADD COLUMN logo_updated_at timestamptz,                     -- versions the public logo URL, so a new logo is seen at once despite caching
    ADD COLUMN accent_color    text CHECK (accent_color ~ '^#[0-9a-f]{6}$'),
    ADD COLUMN website_url     text CHECK (char_length(website_url) <= 2048 AND website_url LIKE 'https://%'),
    ADD COLUMN hide_powered_by boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT status_pages_logo_consistent CHECK ((logo IS NULL) = (logo_type IS NULL) AND (logo IS NULL) = (logo_width IS NULL)
        AND (logo IS NULL) = (logo_height IS NULL) AND (logo IS NULL) = (logo_updated_at IS NULL));

ALTER TABLE plans
    ADD COLUMN status_page_branding    boolean NOT NULL DEFAULT false, -- logo, accent color and website link
    ADD COLUMN status_page_white_label boolean NOT NULL DEFAULT false; -- hide "Powered by HawkEye"
UPDATE plans SET status_page_branding = true WHERE code IN ('pro', 'agency');
UPDATE plans SET status_page_white_label = true WHERE code = 'agency';
