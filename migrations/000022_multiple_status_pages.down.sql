ALTER TABLE plans DROP COLUMN max_status_pages;
-- Going back to one page per organization keeps each organization's oldest page.
DELETE FROM status_pages p WHERE EXISTS (
    SELECT 1 FROM status_pages o WHERE o.organization_id = p.organization_id AND (o.created_at, o.id) < (p.created_at, p.id));
DROP INDEX status_pages_organization_idx;
ALTER TABLE status_pages ADD CONSTRAINT status_pages_organization_id_key UNIQUE (organization_id);
