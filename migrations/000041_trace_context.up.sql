-- Trace context for website and API checks: when trace_context is on, each check sends a W3C traceparent header with a new trace ID, so the request
-- shows up in the customer's own tracing tool. Off by default. The trace ID is kept with the result so a failed check can be looked up there.
ALTER TABLE monitors ADD COLUMN trace_context boolean NOT NULL DEFAULT false;
ALTER TABLE check_results ADD COLUMN trace_id text CHECK (trace_id ~ '^[0-9a-f]{32}$');
