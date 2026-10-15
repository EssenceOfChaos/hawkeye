-- Monitor credentials are now stored encrypted ("enc:v1:<base64>", AES-256-GCM), which is longer than the plain value.
-- Existing plain-text values are encrypted by the API at startup (monitors.Service.EncryptLegacySecrets).
ALTER TABLE monitors DROP CONSTRAINT monitors_auth_secret_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_auth_secret_check CHECK (char_length(auth_secret) <= 12000);
