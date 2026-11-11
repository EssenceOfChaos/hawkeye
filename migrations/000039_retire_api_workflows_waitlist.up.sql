-- Multi-step API checks launched (migration 000038), so their "Get notified" waitlist is retired: its sign-ups were emailed and removed by hand
-- (docs/operations.md, Feature waitlists), and the key is no longer accepted.
DELETE FROM waitlist_entries WHERE feature = 'api_workflows';
ALTER TABLE waitlist_entries
    DROP CONSTRAINT waitlist_entries_feature_check,
    ADD CONSTRAINT waitlist_entries_feature_check CHECK (feature IN ('status_page_domains', 'status_page_subscribers'));
