DELETE FROM waitlist_entries WHERE feature = 'browser_checks';
ALTER TABLE waitlist_entries
    DROP CONSTRAINT waitlist_entries_feature_check,
    ADD CONSTRAINT waitlist_entries_feature_check CHECK (feature IN ('status_page_subscribers'));
