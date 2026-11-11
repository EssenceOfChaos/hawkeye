-- Times are Unix seconds. Checks are raw results (kept 14 days, for looking into a blip); outages are the confirmed periods the uptime is
-- computed from (kept for good); incidents are notes we write ourselves.
CREATE TABLE checks (
    component TEXT NOT NULL,
    at        INTEGER NOT NULL,
    ok        INTEGER NOT NULL,
    ms        INTEGER,
    detail    TEXT,
    PRIMARY KEY (component, at)
);
CREATE TABLE component_state (
    component        TEXT PRIMARY KEY,
    failures         INTEGER NOT NULL DEFAULT 0,
    first_failure_at INTEGER,
    down_since       INTEGER
);
CREATE TABLE outages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    component  TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER
);
CREATE INDEX outages_component_idx ON outages (component, started_at);
CREATE TABLE heartbeats (
    region          TEXT PRIMARY KEY,
    at              INTEGER NOT NULL,
    overdue_seconds INTEGER NOT NULL
);
CREATE TABLE incidents (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    title       TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    resolved_at INTEGER
);
CREATE TABLE incident_updates (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id INTEGER NOT NULL REFERENCES incidents (id),
    status      TEXT NOT NULL CHECK (status IN ('investigating', 'identified', 'monitoring', 'resolved')),
    message     TEXT NOT NULL,
    created_at  INTEGER NOT NULL
);
-- measured_since: when the first check ran; uptime is never counted from before it.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);
