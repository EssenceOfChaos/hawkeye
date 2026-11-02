-- An organization may now have several status pages (an agency gives each client its own), up to its plan's limit. Slugs stay unique across
-- the whole installation, since they are the public address.
ALTER TABLE status_pages DROP CONSTRAINT status_pages_organization_id_key;
CREATE INDEX status_pages_organization_idx ON status_pages (organization_id, created_at);

ALTER TABLE plans ADD COLUMN max_status_pages integer NOT NULL DEFAULT 1 CHECK (max_status_pages >= 0);
UPDATE plans SET max_status_pages = 3 WHERE code = 'pro';
UPDATE plans SET max_status_pages = 50 WHERE code = 'agency';
