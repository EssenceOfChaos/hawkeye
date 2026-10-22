# Operations (monitoring engine)

Starts with Phase 4; extended in Phase 8.

## Processes

| Process | Command | Notes |
|---|---|---|
| API | `cmd/api` (:8080) | readiness `/readyz` |
| Worker | `cmd/worker` (metrics :9090) | scheduler + check workers. Run 1..N instances; all are safe to run concurrently. `/healthz` on :9090 checks the database |

## Metrics (`GET :9090/metrics`, Prometheus format)

| Metric | Meaning / alert idea |
|---|---|
| `hawkeye_canary_up` | 1 while this worker reaches independent public sites, 0 while it does not (the canary). Alert if 0 for 3 min |
| `hawkeye_checks_total{result="inconclusive",category}` | failures dropped (never recorded) because the canary said our own network was down |
| `hawkeye_scheduler_lag_seconds` | age of the most overdue monitor. Alert if > 2 × the shortest interval (60 s plans: > 120 s) |
| `hawkeye_queue_depth{state}`, `hawkeye_queue_oldest_job_age_seconds` | work waiting / running. Oldest age growing = workers too few or stuck |
| `hawkeye_scheduler_backpressure_total` | queue hit `QUEUE_MAX_DEPTH`; add workers or raise concurrency |
| `hawkeye_scheduler_missed_checks_total` | checks scheduled a full interval late (the engine was behind or down) |
| `hawkeye_scheduler_skipped_total` | due but the previous check was still outstanding |
| `hawkeye_checks_total{result,category}` | rate of checks; failures by category (`dns`, `connect`, `tls`, `timeout`, `redirect`, `blocked`, `http_status`, `other`) |
| `hawkeye_check_duration_seconds` | histogram of check wall time |
| `hawkeye_jobs_reclaimed_total` | jobs picked up after a worker died or a retry; a steady trickle after deploys is normal, a sustained rate is not |
| `hawkeye_jobs_dropped_total` | jobs given up after `JOB_MAX_ATTEMPTS` — investigate worker errors |
| `hawkeye_worker_errors_total{stage}` | internal errors (`claim`, `load`, `record`, `complete`, `sample`, `schedule`) |
| `hawkeye_worker_last_poll_timestamp_seconds` | alert if older than ~1 minute: workers are not polling |

Monitor the engine from outside the application (an external uptime check on `/healthz` and `/readyz`); the product must never be the
only thing that can notice its own outage.

| `hawkeye_notification_deliveries_total{event,result}` | `sent`, `retry`, `failed`, `skipped`. Alert on a sustained `failed` or `retry` rate: email provider trouble |
| `hawkeye_notification_pending`, `hawkeye_notification_oldest_pending_seconds` | alerts waiting; the oldest due one should stay under a minute or two |

| `hawkeye_domain_lookups_total{result}` | domain expiry lookups: `known`, `unavailable` (the registry gives no date: normal for some TLDs), `error` (transient). Alert if `error` dominates for hours: a registry, IANA or the worker's network is failing |
| `hawkeye_retention_last_success_timestamp_seconds` | alert if older than ~3 × `RETENTION_INTERVAL`: retention is not completing and the database will grow |
| `hawkeye_retention_deleted_rows_total{kind}`, `hawkeye_retention_runs_total{result}`, `hawkeye_retention_errors_total` | what is being removed; `skipped` means another worker held the lock (normal with several workers) |
| `hawkeye_retention_last_duration_seconds` | a pass that takes much longer than usual may mean a backlog (e.g. a large downgrade) |

| `hawkeye_billing_webhook_events_total{result}` | `received`, `duplicate`, `invalid_signature`, `processed`, `ignored`, `retry`, `failed`. A burst of `invalid_signature` = wrong `STRIPE_WEBHOOK_SECRET` (after rolling it?) or probing |
| `hawkeye_billing_events_pending`, `hawkeye_billing_oldest_pending_seconds` | stored events not yet applied; the oldest should stay under a few minutes |
| `hawkeye_billing_risk_events_total{type}` | **alert on any increase**: a dispute, refund or fraud warning needs human review (runbook in `docs/billing.md`) |
| `hawkeye_billing_events_failed` | **alert on > 0**: an event gave up (8 attempts) and a customer's plan may be wrong |

## API metrics (`:9091/metrics`, API process, private)

The API serves its metrics on a **separate port that is never published or proxied** (Caddy forwards only `/api/*`, `/healthz`, `/readyz`). Scrape it from inside the Docker network.

| Metric | Meaning |
|---|---|
| `hawkeye_http_requests_total{method,route,code}` | requests by route *template* (e.g. `/api/v1/monitors/:monitorID`), so cardinality stays bounded |
| `hawkeye_http_request_duration_seconds{route}` | latency histogram |
| `hawkeye_http_requests_in_flight`, `hawkeye_http_panics_total` | load; recovered handler panics (each also goes to the error tracker) |
| `hawkeye_rate_limited_total{scope}` | requests refused by a rate limit (`global`, `route:login`, `login-account`, ...). A spike on `route:login` is credential stuffing |
| `hawkeye_rate_limiter_errors_total` | the shared limiter's database failed and limits were skipped (fail-open) |
| `hawkeye_email_actions_suppressed_total{action}` | emails not sent because an address hit its hourly cap (mail-bombing attempts) |
| `hawkeye_db_pool_*` | connection pool usage (`acquired`, `idle`, `total`, `max`, `empty_acquires_total`, `acquire_wait_seconds_total`); also exposed by the worker |
| `hawkeye_billing_webhook_events_total{result}`, `hawkeye_billing_risk_events_total{type}` | billing intake (the API receives webhooks) |

## Alerting and external monitoring

`deploy/prometheus-alerts.yml` contains 21 alert rules covering API errors and latency, panics, the shared limiter, database pool pressure, workers not polling, scheduler lag,
queue backlog/backpressure, missed checks, dropped jobs, mass check failures (a sign of **our** network problem), stale retention, alert-email backlog/failures, and billing
failures/risk events. They are validated with `promtool check rules`, and a unit test fails if a rule names a metric the code does not export.
Load the file into your Prometheus (or Grafana/Alertmanager) and route `severity: page` alerts to a pager.

Also run an **external** uptime check on `https://<domain>/readyz` from a different provider/region (several free tiers exist), alerting a channel that does not depend on this
system. A monitoring product must never be the only thing able to notice its own outage. Optional: set `SENTRY_DSN` for error tracking (events are scrubbed of emails, tokens and keys before leaving the process). See *Sentry shows nothing* below to check it.

## Runbooks

Each section is linked from an alert in `deploy/prometheus-alerts.yml`.

### API errors

1. `docker compose logs --tail 200 api` (JSON; every request has a `request_id`, which is also in the `X-Request-Id` response header and in error-tracker events).
2. 5xx on one route only → look at that route's handler/logs. 5xx everywhere → check `/readyz` (database) and `hawkeye_db_pool_*` (pool exhausted? raise `DB_MAX_CONNS`, check for slow queries with `SELECT * FROM pg_stat_activity WHERE state <> 'idle' ORDER BY query_start`).
3. Latency up with CPU low → database. Latency up with CPU high on the API container → scale `api` replicas (they are stateless; sessions and rate limits are in PostgreSQL).
4. A burst of 429s: see `hawkeye_rate_limited_total` by scope; legitimate NAT'd offices can hit the per-IP limits (raise `AUTH_RATE_LIMIT_PER_MIN` / `GLOBAL_RATE_LIMIT_PER_MIN`).

### Email delivery failing

Symptoms: `HawkEyeAlertEmailBacklog`/`HawkEyeAlertEmailFailing`. Delivery errors are in `notification_deliveries.last_error` (sanitised) and the worker log.
`smtp auth` errors → wrong `SMTP_USERNAME/PASSWORD` (rotated key?); `starttls`/certificate errors → wrong `SMTP_TLS` mode/port or a private CA (`SMTP_CA_FILE`); `451/452` → provider throttling (retried automatically with backoff);
`550/553` → bad recipient (marked failed, no retry). Deliveries retry for hours, so fixing the credential lets queued alerts go out; to push them now:
`UPDATE notification_deliveries SET next_attempt_at = now() WHERE status = 'pending';`. Account emails (verification, reset) are sent by the API process; their failures are logged as `send ... email failed`.

## Troubleshooting

More specific runbooks:

### Certificate warnings are not arriving

Warnings use the same queue as outage alerts, so first follow *Alerts are not arriving*. Then check what the monitor knows: `SELECT ssl_expires_at, ssl_checked_at, ssl_notified_bucket, ssl_notified_for FROM monitors WHERE id = '…';`.
`ssl_expires_at` null means no check has read a certificate (an `http://` URL, a failing TLS handshake that is not an expiry, or no check has run yet). `ssl_notified_bucket` is the smallest warning bucket already queued for that certificate;
the delivery rows are `SELECT * FROM notification_deliveries WHERE event_type = 'ssl_expiry' AND monitor_id = '…';`. A bucket is handled even if nobody could be notified at the time (no verified, enabled recipient), and is never re-sent: to repeat a warning,
`UPDATE monitors SET ssl_notified_bucket = NULL, ssl_notified_for = NULL WHERE id = '…';` and the next check queues it again for the current bucket.

### Domain expiry is missing or wrong

Warnings use the same queue as outage alerts, so first follow *Alerts are not arriving*. Then ask what HawkEye knows: `SELECT name, status, expires_at, source, unavailable_reason, checked_at, failure_count, last_error, next_check_at, notified_bucket FROM domains WHERE organization_id = '…';`.
`status = 'pending'` with `next_check_at` in the future means it has not been looked up yet (workers run the job every `DOMAIN_CHECK_INTERVAL`, default 10 m; check `DISABLE_DOMAIN_CHECKS` and that a worker is up). A domain beyond the plan's `max_domains` (the oldest are tracked) is listed but never looked up.
`status = 'unavailable'` is the registry's answer, not a fault: see `unavailable_reason`. To see what the registry says, `curl -s https://rdap.org/domain/example.com | jq '.events'` (RDAP) or `whois example.com | grep -i expir` (WHOIS, which the worker reads on port 43, so that must be reachable from the worker network). WHOIS output differs by registry; if a registry writes the date in a form `ParseWHOISExpiry` does not know, add the key or layout in `internal/domains/whois.go`.
`failure_count > 0` with `last_error` means the lookups are failing (it keeps showing the last good date): a timeout or rate limit from the registry, or a blocked address (`destination not permitted`, which would mean the registry resolved to a private address). A bucket is handled even if nobody could be notified at the time and is never re-sent; to repeat a warning,
`UPDATE domains SET notified_bucket = NULL, notified_for = NULL WHERE id = '…';` and the next lookup queues it again for the current bucket. To look a domain up now, `UPDATE domains SET next_check_at = now() WHERE id = '…';`.

### Sentry shows nothing

Sentry receives only unexpected *errors* (uncaught API errors and panics, worker/scheduler/retention/domain-job failures, permanently failed alert deliveries, billing webhook failures). It gets no logs, no performance data and nothing from the browser, and an ordinary customer-site outage, a 4xx or a failed login is not an error, so an empty Issues list on a healthy system is normal.
To check the setup, with the compose command used by `scripts/deploy.sh`:

1. `... logs api worker | grep "error tracking"`: `error tracking enabled sentry_host=… sentry_project=…` shows where events go (the project number must be the one you are looking at in sentry.io); `error tracking disabled` means `SENTRY_DSN` is empty in `.env.production`; `does not look like a DSN` means it is mangled. The key part of the DSN is never logged.
2. `... exec api /usr/local/bin/api sentry-test` sends one labelled test event and says whether Sentry accepted it. It exits 0 only when it was delivered, and prints the SDK's complaint otherwise (a wrong key or a deleted project gives a refusal, a blocked network a timeout). The event appears under Issues, tagged `component=sentry-test`, within a minute.
3. If the test is delivered but real errors never appear, that is usually good news. Also check the environment filter in Sentry (events are tagged with `APP_ENV`, normally `production`).

### Stored credentials cannot be opened

Alert `HawkEyeCredentialsUnreadable` (`hawkeye_worker_errors_total{stage="secrets"}`), worker log line `cannot open monitor credentials`. Monitors that use authentication are **not being checked**: the
worker retries them and records no result, deliberately, so customers get no false outage alerts. Cause: the worker's `SECRETS_ENCRYPTION_KEY` is missing, differs from the API's, or was changed after
credentials were saved (the error says "wrong key or value moved"). Fix: restore the original key in `.env.production` for **both** `api` and `worker` and restart them; checks resume on their own.
If the key is truly lost, the stored credentials cannot be recovered: clear them (`UPDATE monitors SET auth_type='none', auth_secret=NULL, auth_username=NULL, auth_header_name=NULL WHERE auth_secret IS NOT NULL;`),
generate a new key (`openssl rand -base64 32`) and tell affected customers to enter their credentials again. Rotating the key on purpose has the same effect, so there is no routine rotation: do it only after a suspected leak.

### Billing is out of sync with Stripe

(1) Look at their organization's row and recent events:
`SELECT s.status, p.code, s.provider_subscription_id, s.last_synced_at FROM subscriptions s JOIN plans p ON p.id = s.plan_id WHERE s.organization_id = '…';` and
`SELECT provider_event_id, event_type, status, attempt_count, last_error, received_at FROM billing_webhook_events ORDER BY received_at DESC LIMIT 20;`
(2) No events at all → the webhook is not reaching us: check the endpoint URL/secret in Stripe (Workbench → Webhooks → Event deliveries), TLS, and `invalid_signature` counts.
(3) Events `received` with `last_error` → processing is failing (Stripe API unreachable, unknown price → check `STRIPE_PRICE_*` on **both** api and worker, conflicting subscription).
(4) Events `failed` → fix the cause, then `UPDATE billing_webhook_events SET status='received', attempt_count=0, next_attempt_at=now() WHERE status='failed' AND id='…';`
(5) Fastest repair for one organization: the owner (or you, via the API as them) calls `POST /api/v1/billing/sync`, which re-reads the subscription from Stripe.

### Database growing

Check `hawkeye_retention_last_success_timestamp_seconds`, the worker log lines "retention pass complete / failed", and that
`DISABLE_RETENTION` is not set on every worker. Table sizes: `SELECT pg_size_pretty(pg_total_relation_size('check_results'));`. A first pass after a long gap deletes in
batches of `RETENTION_BATCH` (default 5000) with 50 ms pauses, so it can take a while but does not block checks or the API. Run a pass by restarting a worker, or lower `RETENTION_INTERVAL` temporarily.

### History is missing

Their plan's window applies (`plans.history_retention_days`): 7 / 90 / 365 days. After a downgrade older data is deleted on the next pass and cannot be restored (restore from a backup if absolutely necessary).

### Uptime shows not enough data

Fewer than 5 completed checks exist in the window (new monitor, paused monitor, or a window beyond the retained history).

### Alerts are not arriving

(1) Is the monitor's incident open? `SELECT * FROM incidents WHERE monitor_id = '…' ORDER BY started_at DESC LIMIT 3;`
(2) Were deliveries created? `SELECT d.event_type, d.status, d.attempt_count, d.last_error, s.destination FROM notification_deliveries d JOIN notification_settings s ON s.id = d.notification_setting_id WHERE d.incident_id = '…';`
No rows means no enabled, verified recipient existed when the incident opened (check `notification_settings.verified_at`/`enabled`).
`pending` with a future `next_attempt_at` is a retry waiting for backoff; `failed` has the sanitised reason in `last_error`.
(3) Is a worker running (the dispatcher lives in `cmd/worker`)? In development the email provider is `console`, so mail appears in the worker log, not a mailbox.
To retry a failed delivery now: `UPDATE notification_deliveries SET status='pending', attempt_count=0, next_attempt_at=now() WHERE id='…';`

### Checks are not running

(1) Is a worker up? `hawkeye_worker_last_poll_timestamp_seconds`, `/healthz`. (2) Is a scheduler running
(`DISABLE_SCHEDULER` unset on at least one instance)? (3) Look at lag and queue metrics:
```sql
SELECT count(*) FILTER (WHERE status='queued') queued, count(*) FILTER (WHERE status='running') running,
       count(*) FILTER (WHERE status='running' AND locked_until < now()) expired_leases FROM check_jobs;
SELECT count(*) AS overdue, min(next_check_at) FROM monitors
 WHERE enabled AND deleted_at IS NULL AND next_check_at <= now();
```
### Jobs stuck running

Expected briefly. If `expired_leases > 0` and nothing reclaims them, no worker is polling. Expired leases are
reclaimed automatically; no manual action is needed. To force a retry: `UPDATE check_jobs SET locked_until = now() - interval '1 second' WHERE status='running';`

### Queue full (backpressure)

Raise `WORKER_CONCURRENCY` (and DB pool), add worker instances, or raise `QUEUE_MAX_DEPTH`. Each check holds one
goroutine and one database connection only while claiming/recording, not while waiting on the website.

### Many blocked failures for one monitor

The target resolves to a private/reserved address or redirects to one; this is the SSRF guard working. Tell the customer.

### Many dns or timeout failures across unrelated sites

Suspect worker-side DNS or egress networking, not the customers' sites. The canary (below) already stops these from becoming false outages.

### Canary: our own network is down

HawkEye checks customers' sites from one server. If that server's network or DNS breaks, every check fails, so the worker double-checks: when a check fails in a way our own network could cause
(`dns`, `connect`, `timeout`, `other`; never an HTTP error status, a failed assertion or a certificate error), it probes three independent public endpoints (Cloudflare, Google, Apple; the same
SSRF-safe client and DNS path customers' checks use). If most of them are unreachable too, the result is **dropped, not recorded**: no failed row in the history, no streak, no incident, no alert,
and the job is completed so the monitor's next scheduled check counts normally. The verdict is reused for 15 s, so a burst of failures causes one round of probes. A background probe every `CANARY_INTERVAL`
(default 60 s) keeps `hawkeye_canary_up` current.

- **Seeing it:** `hawkeye_canary_up == 0` (alert `HawkEyeCanaryImpaired`), `hawkeye_checks_total{result="inconclusive"}`, and the worker log lines `canary: this server cannot reach ...` / `the network is back`.
- **What to do:** it is a problem on the server, not on customers' sites. Check DNS from the worker network (`docker run --rm --network hawkeye_checks curlimages/curl -sI https://www.cloudflare.com`), the egress filter
  (`scripts/verify-egress.sh`; the resolver IPs in `ALLOW_DNS` must be the server's real ones) and the provider's status page. Monitors are not being checked meanwhile; they catch up when it recovers.
- **Settings:** `CANARY_URLS` (comma-separated https URLs; empty = the three built-in ones; `off` disables the canary and logs a warning at startup) and `CANARY_INTERVAL` (at least 10 s). A strict majority of the
  endpoints must answer for the network to count as healthy. Using endpoints you do not control means they can change: if one is retired, replace it here.
- **Limits:** a monitored site that is truly down at the same moment our network fails is noticed on its next check once the canary recovers. A canary outage never hides a real failure it cannot rule out: an
  inconclusive canary (cancelled, or no endpoints configured) counts as healthy.

### Wipe a poisoned queue

(safe: the scheduler re-enqueues due monitors): `DELETE FROM check_jobs;`

## Tuning

`JOB_LEASE` must exceed the longest check: 30 s max timeout plus connection setup. Raising `FAILURE_THRESHOLD` reduces false alarms and delays detection by one interval per step.
