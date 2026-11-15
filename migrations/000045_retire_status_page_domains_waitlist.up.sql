-- Status pages on customers' own domains launched (migration 000044), so their "Get notified" waitlist is retired: its sign-ups are emailed and
-- removed by hand before this release (docs/operations.md, Feature waitlists), and the key is no longer accepted.
DELETE FROM waitlist_entries WHERE feature = 'status_page_domains';
ALTER TABLE waitlist_entries
    DROP CONSTRAINT waitlist_entries_feature_check,
    ADD CONSTRAINT waitlist_entries_feature_check CHECK (feature IN ('status_page_subscribers'));
