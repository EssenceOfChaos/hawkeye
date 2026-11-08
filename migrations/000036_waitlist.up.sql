-- Waitlists for features that are coming soon ("Get notified" on the landing page): who wants to hear when each one launches. An address typed in
-- while signed out counts only once its owner confirms it from an email (anyone can type any address into a public form); a signed-in person with a
-- verified address is added at once. Addresses are used only to announce that feature, and remove_token (in every email) takes one off the list.
CREATE TABLE waitlist_entries (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature            text NOT NULL CHECK (feature IN ('api_workflows', 'status_page_domains', 'status_page_subscribers')),
    email              text NOT NULL CHECK (email = lower(email) AND char_length(email) BETWEEN 3 AND 254),
    user_id            uuid REFERENCES users (id) ON DELETE CASCADE,
    confirm_token_hash bytea UNIQUE,
    confirm_expires_at timestamptz,
    confirmed_at       timestamptz,
    remove_token       uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (feature, email)
);
CREATE INDEX waitlist_entries_email_idx ON waitlist_entries (email);
