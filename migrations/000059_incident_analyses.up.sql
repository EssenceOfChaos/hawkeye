-- AI incident analysis (docs/plan-ai-incident-analysis.md): a short "likely cause" written by Claude from an incident's failure snapshot.

-- One per incident. The row is also the job: it is inserted 'pending' in the transaction that opens the incident (or when someone asks for an
-- analysis), and the worker claims it, calls the model and stores the result. Deleted with its incident.
CREATE TABLE incident_analyses (
    incident_id     uuid PRIMARY KEY REFERENCES incidents (id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- pending and running: waiting for or with the worker. done: the result below. declined: the model declined. failed: the model could not be
    -- reached after every attempt. over_limit: the month's allowance was used up. off: the organization turned analysis off, or its plan no
    -- longer includes it, before the job ran.
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'running', 'done', 'declined', 'failed', 'over_limit', 'off')),
    attempts        smallint NOT NULL DEFAULT 0,
    run_at          timestamptz NOT NULL DEFAULT now(),
    locked_until    timestamptz,
    -- When this run was asked for: the usage row of the run is keyed by it, so a job claimed again after a crash is not counted twice.
    requested_at    timestamptz NOT NULL DEFAULT now(),
    category        text,
    likely_cause    text,
    evidence        jsonb,
    next_step       text,
    confidence      text CHECK (confidence IN ('low', 'medium', 'high')),
    model           text,
    input_tokens    integer,
    output_tokens   integer,
    finished_at     timestamptz
);
CREATE INDEX incident_analyses_runnable ON incident_analyses (run_at) WHERE status IN ('pending', 'running');
CREATE INDEX incident_analyses_org ON incident_analyses (organization_id);

-- One row per analysis that counted against the monthly allowance (calendar month, UTC). Re-runs count; declined and failed runs are removed
-- again. Not tied to the incident, so deleting an incident gives nothing back within the month. Rows older than two months are deleted by retention.
CREATE TABLE ai_analysis_usage (
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    incident_id     uuid NOT NULL,
    requested_at    timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (incident_id, requested_at)
);
CREATE INDEX ai_analysis_usage_org ON ai_analysis_usage (organization_id, created_at);

-- Analyses a month on each plan.
ALTER TABLE plans ADD COLUMN max_ai_analyses integer NOT NULL DEFAULT 0 CHECK (max_ai_analyses >= 0);
UPDATE plans SET max_ai_analyses = 25 WHERE code = 'free';
UPDATE plans SET max_ai_analyses = 250 WHERE code = 'pro';
UPDATE plans SET max_ai_analyses = 1000 WHERE code = 'agency';

-- Opt-in: off for every organization until its owner turns it on.
ALTER TABLE organizations ADD COLUMN ai_analysis_enabled boolean NOT NULL DEFAULT false;
