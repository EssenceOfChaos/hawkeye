UPDATE check_results SET error_category = 'other' WHERE error_category = 'assertion';
ALTER TABLE check_results DROP CONSTRAINT check_results_error_category_check;
ALTER TABLE check_results ADD CONSTRAINT check_results_error_category_check CHECK (error_category IN
    ('dns', 'connect', 'tls', 'timeout', 'redirect', 'blocked', 'http_status', 'other'));

ALTER TABLE monitors
    DROP CONSTRAINT monitors_auth_consistent,
    DROP COLUMN assertions,
    DROP COLUMN auth_secret,
    DROP COLUMN auth_header_name,
    DROP COLUMN auth_username,
    DROP COLUMN auth_type,
    DROP COLUMN request_body,
    DROP COLUMN request_headers;

-- Monitors using the new methods fall back to GET rather than blocking the rollback.
UPDATE monitors SET method = 'GET' WHERE method NOT IN ('GET', 'HEAD');
ALTER TABLE monitors DROP CONSTRAINT monitors_method_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_method_check CHECK (method IN ('GET', 'HEAD'));
