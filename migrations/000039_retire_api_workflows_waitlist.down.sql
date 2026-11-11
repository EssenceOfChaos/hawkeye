ALTER TABLE waitlist_entries
    DROP CONSTRAINT waitlist_entries_feature_check,
    ADD CONSTRAINT waitlist_entries_feature_check CHECK (feature IN ('api_workflows', 'status_page_domains', 'status_page_subscribers'));
