-- API checks: more methods, custom headers, a request body, authentication and response body assertions.
ALTER TABLE monitors DROP CONSTRAINT monitors_method_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_method_check
    CHECK (method IN ('GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'));

ALTER TABLE monitors
    -- [{"name": "...", "value": "..."}]; values are shown to everyone in the organization, so secrets belong in auth_secret.
    ADD COLUMN request_headers  jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(request_headers) = 'array'),
    ADD COLUMN request_body     text CHECK (octet_length(request_body) <= 65536),
    ADD COLUMN auth_type        text NOT NULL DEFAULT 'none' CHECK (auth_type IN ('none', 'basic', 'bearer', 'header')),
    ADD COLUMN auth_username    text CHECK (char_length(auth_username) <= 200),
    ADD COLUMN auth_header_name text CHECK (char_length(auth_header_name) <= 100),
    -- Write-only through the API: it is used by checks and never returned or exported.
    ADD COLUMN auth_secret      text CHECK (char_length(auth_secret) <= 2048),
    -- [{"type": "body_contains"|"body_not_contains"|"json_path", "path": "...", "operator": "...", "value": "..."}]
    ADD COLUMN assertions       jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(assertions) = 'array'),
    ADD CONSTRAINT monitors_auth_consistent CHECK (
        (auth_type = 'none' AND auth_secret IS NULL) OR (auth_type <> 'none' AND auth_secret IS NOT NULL));

ALTER TABLE check_results DROP CONSTRAINT check_results_error_category_check;
ALTER TABLE check_results ADD CONSTRAINT check_results_error_category_check CHECK (error_category IN
    ('dns', 'connect', 'tls', 'timeout', 'redirect', 'blocked', 'http_status', 'assertion', 'other'));
