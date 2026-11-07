-- Incident updates: short notes an owner posts on an incident ("Investigating", "Fixed"). They appear under the incident on every published status page
-- that lists its monitor, so they are public text. They are plain text: the API never turns them into markup, and the page shows them as text.
-- They go with their incident (retention, monitor or account deletion); a departed author leaves the update in place, without a name.
CREATE TABLE incident_updates (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id uuid NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    status      text NOT NULL CHECK (status IN ('investigating', 'identified', 'monitoring', 'resolved')),
    message     text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 1000),
    author_id   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX incident_updates_incident_idx ON incident_updates (incident_id, created_at);
