-- Encrypted credentials cannot be turned back into plain text without the key, so rolling back discards them: those monitors lose their
-- authentication and must be given their credentials again. Their auth type is reset so the consistency constraint holds.
UPDATE monitors SET auth_type = 'none', auth_secret = NULL, auth_username = NULL, auth_header_name = NULL WHERE auth_secret LIKE 'enc:v1:%';
ALTER TABLE monitors DROP CONSTRAINT monitors_auth_secret_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_auth_secret_check CHECK (char_length(auth_secret) <= 2048);
