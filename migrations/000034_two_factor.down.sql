ALTER TABLE organizations DROP COLUMN require_two_factor_since;
DROP TABLE login_challenges;
DROP TABLE recovery_codes;
ALTER TABLE users DROP CONSTRAINT users_totp_consistent,
    DROP COLUMN totp_secret, DROP COLUMN totp_pending_secret, DROP COLUMN totp_enabled_at, DROP COLUMN totp_last_step;
