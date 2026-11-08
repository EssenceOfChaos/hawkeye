DELETE FROM api_tokens WHERE kind = 'oauth_access';
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_oauth_grant, DROP COLUMN oauth_grant_id, DROP CONSTRAINT api_tokens_kind_check;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_kind_check CHECK (kind IN ('key'));
DROP TABLE oauth_refresh_tokens;
DROP TABLE oauth_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_clients;
