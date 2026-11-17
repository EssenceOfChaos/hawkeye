-- NOT VALID, so rolling back keeps any workflow_config results already recorded.
ALTER TABLE check_results DROP CONSTRAINT check_results_error_category_check;
ALTER TABLE check_results ADD CONSTRAINT check_results_error_category_check CHECK (error_category IN
    ('dns', 'connect', 'tls', 'timeout', 'redirect', 'blocked', 'http_status', 'assertion', 'other', 'missed_ping', 'job_failed')) NOT VALID;
