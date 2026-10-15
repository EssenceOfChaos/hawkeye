-- One public status page per organization, shown at /status/<slug> to anyone with the link once published.
-- Only the monitors the owner explicitly adds appear, under the name they choose; URLs and error details are never exposed.
CREATE TABLE status_pages (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL UNIQUE REFERENCES organizations (id) ON DELETE CASCADE,
    slug            text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    title           text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 100),
    description     text NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    published       boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER status_pages_set_updated_at BEFORE UPDATE ON status_pages
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE status_page_monitors (
    page_id      uuid NOT NULL REFERENCES status_pages (id) ON DELETE CASCADE,
    monitor_id   uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    display_name text CHECK (display_name IS NULL OR char_length(display_name) BETWEEN 1 AND 100),
    position     integer NOT NULL,
    PRIMARY KEY (page_id, monitor_id)
);
CREATE INDEX status_page_monitors_monitor_idx ON status_page_monitors (monitor_id);
