-- The weekly summary email: every Monday, each verified member of an organization that has monitors gets a short summary of the past week. It is on
-- by default and each person can turn it off (Account → Email preferences, or the link in the email); that stops only this email.
ALTER TABLE users ADD COLUMN weekly_summary_opt_out_at timestamptz;

-- What has been sent: one row per person, organization and week (the Monday the week began, UTC). The row is the claim that makes a send happen at
-- most once, even with several workers.
CREATE TABLE weekly_summaries (
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    week            date        NOT NULL CHECK (extract(isodow FROM week) = 1),
    sent_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, organization_id, week)
);
CREATE INDEX weekly_summaries_week_idx ON weekly_summaries (week);
