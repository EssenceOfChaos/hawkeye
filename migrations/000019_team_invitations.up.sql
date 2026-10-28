-- Team invitations: an owner invites a colleague by email; the colleague follows the link, signs in (or creates an account) with that address and
-- joins the organization as a member. A row exists only while an invitation is pending: accepting or revoking it deletes the row, and an expired
-- one is ignored (and replaced if the same address is invited again), so there is no status column to keep in step.
CREATE TABLE organization_invitations (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    email           text        NOT NULL CHECK (email = lower(btrim(email)) AND char_length(email) <= 254),
    -- Only a SHA-256 hash of the link's token is stored, like sessions and email verification: a database leak does not expose usable links.
    token_hash      bytea       NOT NULL UNIQUE,
    -- Who sent it. Kept as a record only: the invitation outlives the sender's membership, so deleting that account just clears the name.
    invited_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    -- One pending invitation per address and organization; inviting again replaces the link.
    CONSTRAINT organization_invitations_unique_email UNIQUE (organization_id, email)
);
