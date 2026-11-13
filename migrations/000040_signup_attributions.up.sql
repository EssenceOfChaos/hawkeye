-- How a person found HawkEye, recorded once when they sign up: the first campaign tag (utm_*, ?ref=) or ad click their browser arrived with, otherwise
-- the site that linked to us, and the page they landed on. Only short labels are kept, never a full address or anything personal (internal/attribution).
-- Nothing is recorded when the visitor turned analytics off. Deleted with the account.
CREATE TABLE signup_attributions (
    user_id       uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    source        text CHECK (char_length(source) <= 100),
    medium        text CHECK (char_length(medium) <= 100),
    campaign      text CHECK (char_length(campaign) <= 100),
    click_id      text CHECK (click_id IN ('google_ads', 'microsoft_ads', 'meta_ads', 'openai_ads')),
    referrer_host text CHECK (char_length(referrer_host) <= 253),
    landing_path  text CHECK (char_length(landing_path) <= 200),
    first_seen_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX signup_attributions_created_at ON signup_attributions (created_at);
