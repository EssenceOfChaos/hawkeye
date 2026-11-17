-- Multi-step API checks record "workflow_config" when a check can't run as written (a value no step saved, a secret that isn't set, a JSON path to
-- save that isn't in the response: checks.CatWorkflowConfig), but migration 000038 never allowed it here. Such a result failed to insert, so the
-- worker retried it and then dropped the job: the failure was never recorded and never alerted. TestEveryFailureCategoryCanBeRecorded now records
-- every category the code produces.
--
-- NOT VALID: the new rule only widens the old one, so existing rows already satisfy it and there is nothing to check; validating would read the
-- whole table while holding the lock that DROP CONSTRAINT takes. New rows are checked as usual.
ALTER TABLE check_results DROP CONSTRAINT check_results_error_category_check;
ALTER TABLE check_results ADD CONSTRAINT check_results_error_category_check CHECK (error_category IN
    ('dns', 'connect', 'tls', 'timeout', 'redirect', 'blocked', 'http_status', 'assertion', 'other', 'missed_ping', 'job_failed', 'workflow_config')) NOT VALID;
