-- DNS monitors cannot exist without these columns; their history goes with them.
DELETE FROM monitors WHERE kind = 'dns';
ALTER TABLE monitors
    DROP CONSTRAINT monitors_dns_fields,
    DROP COLUMN dns_expected,
    DROP COLUMN dns_record_type,
    DROP CONSTRAINT monitors_kind_check,
    ADD CONSTRAINT monitors_kind_check CHECK (kind IN ('http', 'heartbeat'));
