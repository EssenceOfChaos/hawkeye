-- Multi-step API checks ("workflows", docs/plan-synthetic-api.md). A workflow is a monitor of kind 'workflow' whose requests are its steps, run in
-- order by internal/checks/workflow.go. The monitor row's own url is the first step's address (shown in lists, and used to track its domain); its
-- other request columns are unused, like a heartbeat's.
ALTER TABLE monitors
    DROP CONSTRAINT monitors_kind_check,
    ADD CONSTRAINT monitors_kind_check CHECK (kind IN ('http', 'heartbeat', 'dns', 'workflow')),
    ADD COLUMN workflow_cookies boolean NOT NULL DEFAULT false;

-- The steps, in order. A step's header values, body, address and authentication may contain {{name}} and {{secret.name}} placeholders. auth_secret
-- is sealed like monitors.auth_secret (bound to the monitor). Steps go with the monitor's row; a deleted monitor's are removed at once (deleteTx).
CREATE TABLE monitor_steps (
    monitor_id           uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    position             smallint NOT NULL CHECK (position BETWEEN 0 AND 9),
    name                 text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    method               text NOT NULL,
    url                  text NOT NULL CHECK (char_length(url) <= 2048),
    headers              jsonb NOT NULL DEFAULT '[]',
    body                 text,
    auth_type            text NOT NULL DEFAULT 'none' CHECK (auth_type IN ('none', 'basic', 'bearer', 'header')),
    auth_username        text,
    auth_header_name     text,
    auth_secret          text,
    expected_status_code integer CHECK (expected_status_code BETWEEN 100 AND 599),
    assertions           jsonb NOT NULL DEFAULT '[]',
    extractions          jsonb NOT NULL DEFAULT '[]',
    timeout_seconds      integer NOT NULL DEFAULT 10 CHECK (timeout_seconds BETWEEN 1 AND 30),
    PRIMARY KEY (monitor_id, position)
);

-- Named values a workflow uses as {{secret.name}}: sealed, never returned by the API, hidden in every result. Removed at once when the monitor is
-- deleted (deleteTx), not when its row is purged. A NULL value is a secret that is declared but not entered yet (a workflow an assistant created
-- through MCP, which never sees secret values): the workflow stays paused until a person enters it in the app.
CREATE TABLE monitor_secrets (
    monitor_id uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (name ~ '^[A-Za-z_][A-Za-z0-9_]{0,39}$'),
    value      text,
    PRIMARY KEY (monitor_id, name)
);

-- What each step of a workflow run did: name, status code, time, and the failure if it failed. Never response content or variable values.
ALTER TABLE check_results ADD COLUMN steps jsonb;

-- Plan limits: how many workflows an organization may have (each also counts as a monitor), and how many steps each may have. 0 turns them off.
ALTER TABLE plans
    ADD COLUMN max_workflows integer NOT NULL DEFAULT 0 CHECK (max_workflows >= 0),
    ADD COLUMN max_workflow_steps integer NOT NULL DEFAULT 0 CHECK (max_workflow_steps BETWEEN 0 AND 10);
UPDATE plans SET max_workflows = 1, max_workflow_steps = 3 WHERE code = 'free';
UPDATE plans SET max_workflows = 5, max_workflow_steps = 10 WHERE code = 'pro';
UPDATE plans SET max_workflows = 50, max_workflow_steps = 10 WHERE code = 'agency';
