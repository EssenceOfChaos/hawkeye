-- "Get notified" for browser checks (docs/plan-browser-checks.md, step 0): the sign-up count decides whether they are built.
ALTER TABLE waitlist_entries
    DROP CONSTRAINT waitlist_entries_feature_check,
    ADD CONSTRAINT waitlist_entries_feature_check CHECK (feature IN ('status_page_subscribers', 'browser_checks'));
