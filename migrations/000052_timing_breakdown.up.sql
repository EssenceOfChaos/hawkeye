-- Timing breakdown (docs/plan-timing-breakdown.md): where a website or API check's time went, each phase summed over every connection the
-- check made (one per redirect). NULL for checks without one (DNS monitors, heartbeats, multi-step checks, whose steps keep their own in steps,
-- and results from before this migration). smallint: a check's timeout is at most 30 s.
--
-- No defaults: adding nullable columns is a metadata-only change, so check_results is not rewritten.
ALTER TABLE check_results
    ADD COLUMN dns_ms     smallint CHECK (dns_ms >= 0),
    ADD COLUMN connect_ms smallint CHECK (connect_ms >= 0),
    ADD COLUMN tls_ms     smallint CHECK (tls_ms >= 0),
    ADD COLUMN wait_ms    smallint CHECK (wait_ms >= 0),
    ADD COLUMN redirects  smallint CHECK (redirects >= 0);

-- Hourly totals of the phases, over the successful (non-re-check) results that have them: phase_count is their number, since older results
-- and other monitor types have none.
ALTER TABLE check_rollups
    ADD COLUMN dns_ms_sum     bigint NOT NULL DEFAULT 0 CHECK (dns_ms_sum >= 0),
    ADD COLUMN connect_ms_sum bigint NOT NULL DEFAULT 0 CHECK (connect_ms_sum >= 0),
    ADD COLUMN tls_ms_sum     bigint NOT NULL DEFAULT 0 CHECK (tls_ms_sum >= 0),
    ADD COLUMN wait_ms_sum    bigint NOT NULL DEFAULT 0 CHECK (wait_ms_sum >= 0),
    ADD COLUMN phase_count    integer NOT NULL DEFAULT 0 CHECK (phase_count BETWEEN 0 AND successes);

-- A slowdown's explanation: which phase accounts for most of the extra time (docs/plan-timing-breakdown.md, "Slow-response alerts").
ALTER TABLE slowdowns ADD COLUMN cause text CHECK (char_length(cause) <= 300);
