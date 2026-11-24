-- X sign-ins go: an account that only had X gets in with "Forgot password". Two-factor sign-ins in progress after X start over (they last minutes).
DELETE FROM user_identities WHERE provider = 'x';
DELETE FROM login_challenges WHERE first_factor = 'x';
ALTER TABLE user_identities DROP CONSTRAINT user_identities_provider_check,
    ADD CONSTRAINT user_identities_provider_check CHECK (provider IN ('google', 'github'));
ALTER TABLE login_challenges DROP CONSTRAINT login_challenges_first_factor_check,
    ADD CONSTRAINT login_challenges_first_factor_check CHECK (first_factor IN ('password', 'google', 'github'));
