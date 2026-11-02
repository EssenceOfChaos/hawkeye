-- An organization has exactly one owner (the person billed, who alone manages billing, alert recipients and status pages). Ownership can now be
-- transferred, so the database enforces "at most one" rather than trusting every code path: a transfer demotes the old owner before promoting the new.
CREATE UNIQUE INDEX organization_members_one_owner ON organization_members (organization_id) WHERE role = 'owner';
