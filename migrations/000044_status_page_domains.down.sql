ALTER TABLE plans DROP COLUMN max_status_page_domains;
ALTER TABLE status_pages DROP COLUMN redirect_to_domain;
DROP TABLE status_page_domains;
ALTER TABLE status_pages DROP CONSTRAINT status_pages_id_organization_key;
