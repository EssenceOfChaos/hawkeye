-- Planned maintenance windows, announced on the page before they start (POST /api/maintenance). Downtime inside a window is shown as maintenance
-- and left out of the uptime; outages are still recorded as they happened. canceled_at: canceled before it started, so it never applied.
CREATE TABLE maintenance (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    title       TEXT NOT NULL,
    message     TEXT NOT NULL DEFAULT '',
    starts_at   INTEGER NOT NULL,
    ends_at     INTEGER NOT NULL CHECK (ends_at > starts_at),
    created_at  INTEGER NOT NULL CHECK (created_at <= starts_at),
    canceled_at INTEGER
);
CREATE INDEX maintenance_ends_idx ON maintenance (ends_at);
