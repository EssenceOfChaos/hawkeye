ALTER TABLE plans DROP COLUMN status_page_branding, DROP COLUMN status_page_white_label;
ALTER TABLE status_pages DROP CONSTRAINT status_pages_logo_consistent,
    DROP COLUMN logo, DROP COLUMN logo_type, DROP COLUMN logo_width, DROP COLUMN logo_height, DROP COLUMN logo_updated_at,
    DROP COLUMN accent_color, DROP COLUMN website_url, DROP COLUMN hide_powered_by;
