ALTER TABLE slowdowns DROP COLUMN cause;
ALTER TABLE check_rollups DROP COLUMN dns_ms_sum, DROP COLUMN connect_ms_sum, DROP COLUMN tls_ms_sum, DROP COLUMN wait_ms_sum, DROP COLUMN phase_count;
ALTER TABLE check_results DROP COLUMN dns_ms, DROP COLUMN connect_ms, DROP COLUMN tls_ms, DROP COLUMN wait_ms, DROP COLUMN redirects;
