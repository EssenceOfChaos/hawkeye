-- Retention deletes by age across monitors, and housekeeping scans expiry columns.
CREATE INDEX check_results_checked_at_idx ON check_results (checked_at);
CREATE INDEX incidents_resolved_at_idx ON incidents (resolved_at) WHERE status = 'resolved';
CREATE INDEX monitors_deleted_at_idx ON monitors (deleted_at) WHERE deleted_at IS NOT NULL;
