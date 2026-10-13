-- Shared, database-backed fixed-window counters so rate limits hold across several API processes.
-- Keys are hashed (no raw IP addresses or emails are stored). Rows older than a day are deleted by the retention job.
CREATE TABLE rate_limits (
    key          text        NOT NULL,
    window_start timestamptz NOT NULL,
    hits         integer     NOT NULL DEFAULT 0,
    PRIMARY KEY (key, window_start)
);
CREATE INDEX rate_limits_window_start_idx ON rate_limits (window_start);
