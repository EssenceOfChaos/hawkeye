-- DNS monitors. HawkEye looks up one record of a host name on a schedule: the host name lives in url (without a scheme), dns_record_type is the record to
-- look up, and dns_expected is what an answer must match (NULL: any answer will do). A failed lookup or a wrong answer is recorded like any failed check,
-- with the existing 'dns' and 'assertion' categories.
ALTER TABLE monitors
    DROP CONSTRAINT monitors_kind_check,
    ADD CONSTRAINT monitors_kind_check CHECK (kind IN ('http', 'heartbeat', 'dns')),
    ADD COLUMN dns_record_type text CHECK (dns_record_type IN ('A', 'AAAA', 'MX', 'NS', 'TXT')),
    ADD COLUMN dns_expected text CHECK (char_length(dns_expected) BETWEEN 1 AND 255),
    ADD CONSTRAINT monitors_dns_fields CHECK (CASE WHEN kind = 'dns' THEN dns_record_type IS NOT NULL
        ELSE dns_record_type IS NULL AND dns_expected IS NULL END);
