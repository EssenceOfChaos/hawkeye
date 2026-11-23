ALTER TABLE login_challenges DROP COLUMN first_factor;
ALTER TABLE sessions DROP COLUMN confirmed_at;
DROP TABLE social_auth_states;
DROP TABLE user_identities;
-- Accounts created with Google or GitHub get a hash no password matches: they can still get in with "Forgot password".
UPDATE users SET password_hash = '!' WHERE password_hash IS NULL;
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
