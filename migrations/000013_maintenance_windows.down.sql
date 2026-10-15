ALTER TABLE check_results DROP COLUMN in_maintenance;
DROP FUNCTION in_maintenance(uuid, uuid, timestamptz);
DROP FUNCTION maintenance_window_active(maintenance_windows, timestamptz);
DROP TABLE maintenance_window_monitors;
DROP TABLE maintenance_windows;
ALTER TABLE plans DROP COLUMN maintenance_enabled;
