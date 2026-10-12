-- When the scheduler should next enqueue a check. New monitors are due immediately.
ALTER TABLE monitors ADD COLUMN next_check_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX monitors_due_idx ON monitors (next_check_at) WHERE enabled AND deleted_at IS NULL;

CREATE TABLE check_results (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    monitor_id       uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    checked_at       timestamptz NOT NULL,
    finished_at      timestamptz NOT NULL,
    success          boolean NOT NULL,
    http_status_code smallint,
    response_time_ms integer CHECK (response_time_ms >= 0),
    error_category   text CHECK (error_category IN
        ('dns', 'connect', 'tls', 'timeout', 'redirect', 'blocked', 'http_status', 'other')),
    -- Short, sanitised text only; never response bodies or raw network errors.
    error_summary    text CHECK (char_length(error_summary) <= 200),
    CONSTRAINT check_results_success_has_no_error CHECK (NOT success OR error_category IS NULL)
);
CREATE INDEX check_results_monitor_checked_idx ON check_results (monitor_id, checked_at DESC);

-- Durable work queue. A monitor has at most one outstanding job (UNIQUE), which bounds the queue by the
-- number of monitors. A job whose lease (locked_until) has expired is reclaimable, so work survives
-- worker crashes and restarts.
CREATE TABLE check_jobs (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    monitor_id   uuid NOT NULL UNIQUE REFERENCES monitors (id) ON DELETE CASCADE,
    status       text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running')),
    run_at       timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    attempts     integer NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX check_jobs_queued_idx ON check_jobs (run_at) WHERE status = 'queued';
CREATE INDEX check_jobs_lease_idx ON check_jobs (locked_until) WHERE status = 'running';
