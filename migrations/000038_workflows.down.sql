DELETE FROM monitors WHERE kind = 'workflow';
ALTER TABLE plans DROP COLUMN max_workflow_steps, DROP COLUMN max_workflows;
ALTER TABLE check_results DROP COLUMN steps;
DROP TABLE monitor_secrets;
DROP TABLE monitor_steps;
ALTER TABLE monitors
    DROP COLUMN workflow_cookies,
    DROP CONSTRAINT monitors_kind_check,
    ADD CONSTRAINT monitors_kind_check CHECK (kind IN ('http', 'heartbeat', 'dns'));
