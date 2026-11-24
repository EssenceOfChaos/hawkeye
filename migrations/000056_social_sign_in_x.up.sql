-- Sign in with X, next to Google and GitHub (docs/plan-social-sign-in.md).
ALTER TABLE user_identities DROP CONSTRAINT user_identities_provider_check,
    ADD CONSTRAINT user_identities_provider_check CHECK (provider IN ('google', 'github', 'x'));
ALTER TABLE login_challenges DROP CONSTRAINT login_challenges_first_factor_check,
    ADD CONSTRAINT login_challenges_first_factor_check CHECK (first_factor IN ('password', 'google', 'github', 'x'));
