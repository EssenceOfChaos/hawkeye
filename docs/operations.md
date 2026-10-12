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

## Troubleshooting

**Alerts are not arriving.** (1) Is the monitor's incident open? `SELECT * FROM incidents WHERE monitor_id = '…' ORDER BY started_at DESC LIMIT 3;`
(2) Were deliveries created? `SELECT d.event_type, d.status, d.attempt_count, d.last_error, s.destination FROM notification_deliveries d JOIN notification_settings s ON s.id = d.notification_setting_id WHERE d.incident_id = '…';`
No rows means no enabled, verified recipient existed when the incident opened (check `notification_settings.verified_at`/`enabled`).
`pending` with a future `next_attempt_at` is a retry waiting for backoff; `failed` has the sanitised reason in `last_error`.
(3) Is a worker running (the dispatcher lives in `cmd/worker`)? In development the email provider is `console`, so mail appears in the worker log, not a mailbox.
To retry a failed delivery now: `UPDATE notification_deliveries SET status='pending', attempt_count=0, next_attempt_at=now() WHERE id='…';`

**Checks are not running.** (1) Is a worker up? `hawkeye_worker_last_poll_timestamp_seconds`, `/healthz`. (2) Is a scheduler running
(`DISABLE_SCHEDULER` unset on at least one instance)? (3) Look at lag and queue metrics:
```sql
SELECT count(*) FILTER (WHERE status='queued') queued, count(*) FILTER (WHERE status='running') running,
       count(*) FILTER (WHERE status='running' AND locked_until < now()) expired_leases FROM check_jobs;
SELECT count(*) AS overdue, min(next_check_at) FROM monitors
 WHERE enabled AND deleted_at IS NULL AND next_check_at <= now();
```
**Jobs stuck "running".** Expected briefly. If `expired_leases > 0` and nothing reclaims them, no worker is polling. Expired leases are
reclaimed automatically; no manual action is needed. To force a retry: `UPDATE check_jobs SET locked_until = now() - interval '1 second' WHERE status='running';`

**Queue full (backpressure).** Raise `WORKER_CONCURRENCY` (and DB pool), add worker instances, or raise `QUEUE_MAX_DEPTH`. Each check holds one
goroutine and one database connection only while claiming/recording, not while waiting on the website.

**Many `blocked` failures for a monitor.** The target resolves to a private/reserved address or redirects to one; this is the SSRF guard working. Tell the customer.

**Many `dns`/`timeout` failures across unrelated sites.** Suspect worker-side DNS or egress networking, not the customers' sites.

**Wipe a poisoned queue** (safe: the scheduler re-enqueues due monitors): `DELETE FROM check_jobs;`

## Tuning

`JOB_LEASE` must exceed the longest check: 30 s max timeout plus connection setup. Raising `FAILURE_THRESHOLD` reduces false alarms and delays detection by one interval per step.
