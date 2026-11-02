-- Tags group an organization's monitors (by client, project or anything else), so a long monitor list can be filtered and changed in bulk.
-- Both sides of monitor_tags always belong to the same organization; the application checks this before linking, like status_page_monitors.
CREATE TABLE tags (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),
    color           text CHECK (color ~ '^#[0-9a-f]{6}$'),
    created_at      timestamptz NOT NULL DEFAULT now()
);
-- "Acme" and "acme" are the same tag.
CREATE UNIQUE INDEX tags_org_name_idx ON tags (organization_id, lower(name));

CREATE TABLE monitor_tags (
    monitor_id uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    tag_id     uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (monitor_id, tag_id)
);
CREATE INDEX monitor_tags_tag_idx ON monitor_tags (tag_id);
