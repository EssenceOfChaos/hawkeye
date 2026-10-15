# Architecture

## Approach

A modular monolith: one Go module, one PostgreSQL database, and separately runnable processes that share
`internal/` packages. No microservices, no Kubernetes.

## Current state (Phases 1–8)

```
cmd/api        REST API process (Echo)
cmd/migrate    applies embedded SQL migrations
internal/auth          register/login/sessions/password reset/email verification (Argon2id, hashed tokens)
internal/organizations tenant access; every query scoped by membership
internal/plans         plan catalogue + the entitlement service (all limit decisions live here)
internal/monitors      monitor CRUD, validation, soft delete, per-organization scoping
cmd/worker             monitoring engine process: scheduler + check workers + metrics endpoint (:9090)
internal/safehttp      SSRF-safe HTTP client (see docs/security.md)
internal/checks        runs one check, classifies errors, applies results to monitor state (`Apply`, `Store.Record`)
internal/queue         durable PostgreSQL job queue (claim / lease / complete / release)
internal/scheduler     finds due monitors, enqueues jobs, jitter, backpressure
internal/worker        bounded-concurrency polling loops, metrics sampler
internal/metrics       Prometheus metrics
internal/incidents     incident engine (opened/resolved inside the result transaction) and incident queries
internal/statuspage public status page (`/status/<slug>`; migration `000014`; 30 s cache; exposes only owner-chosen monitor names, states, daily uptime and incident times), internal/maintenance windows (rule in SQL: `in_maintenance`, migration `000013`; `checks.Store.Record` skips state changes and incidents while one applies), internal/notifications recipients + confirmation, enqueue (idempotent), email rendering, webhook/Slack channels (`channels.go`: sealed URLs, signing, SSRF-safe delivery; migration `000012`), retrying dispatcher
internal/reports       range resolution (plan-clamped), check-based uptime, bucketed series, check history, dashboard summary
internal/retention     batched per-plan data retention + credential housekeeping (runs inside cmd/worker)
internal/billing       Stripe: provider interface (+ SDK implementation and fake), checkout/portal/cancel/resync, signed webhook intake, event processing
internal/email         Sender interface + console and in-memory senders
internal/validation    field-level validation error
internal/testutil      per-test isolated, migrated PostgreSQL databases
internal/config    env loading + validation (fails fast, no production fallbacks)
internal/logging   slog (JSON in production, text otherwise)
internal/database  pgx pool + golang-migrate
internal/httpapi   Echo wiring: request ID, logging, recover, body limit, timeout, error envelope, /healthz, /readyz
migrations/    versioned SQL, embedded via go:embed
web/           React + TypeScript (Vite, Vitest): auth pages, monitor list with usage meter, monitor form
```

- `/healthz` is liveness only. `/readyz` pings PostgreSQL (2s timeout) and returns 503 without internal details on failure.
- All errors use `{"error": {"code", "message", "details"}}`; unhandled errors are logged server-side and returned as `INTERNAL_ERROR`.

## Monitoring engine (Phase 4)

```
monitors.next_check_at ──► scheduler.Tick ──► check_jobs ──► worker.ProcessOne ──► check_results
   (due, enabled,           (SKIP LOCKED,      (UNIQUE per     (Claim w/ lease,     + monitors.current_status,
    not deleted)             jitter, cap)       monitor)        safehttp check)      counters, last_*_at
```

- **Scheduling:** `Tick` locks due monitors with `FOR UPDATE SKIP LOCKED`, so any number of schedulers cooperate. The effective
  interval is `max(stored, plan minimum)`, evaluated by `plans.EffectivePlan` at scheduling time. The next run is
  `now + interval + jitter` (jitter up to 10 % of the interval, at most 30 s); after downtime there is no catch-up burst.
- **Queue durability:** jobs live in PostgreSQL. A claim sets a lease (`JOB_LEASE`, default 2 m, validated ≥ 1 m so it exceeds the longest
  possible check). If a worker dies, the lease expires and another worker reclaims the job. Jobs that exhaust `JOB_MAX_ATTEMPTS`
  are dropped (the scheduler re-enqueues the monitor on its next interval, so nothing is permanently lost).
- **Bounded queue:** one outstanding job per monitor (`UNIQUE(monitor_id)`), plus `QUEUE_MAX_DEPTH`. At the cap the scheduler stops
  enqueuing (backpressure metric) and monitors simply become overdue (scheduler-lag metric) rather than the queue growing.
- **Worker:** `WORKER_CONCURRENCY` loops; shutdown stops claiming but lets in-flight checks finish and be recorded.
  Results for monitors deleted or paused mid-flight are discarded.
- **Transactionality:** `Store.Record` locks the monitor row, inserts the `check_results` row and updates status and counters in one transaction.
- **What counts as down (documented, explicit):** transport failures (DNS, connect, TLS, timeout, blocked, too many redirects) always
  fail a check. For HTTP responses, with `expected_status_code` set the final status must equal it; otherwise any final status
  below 400 is success (`500` is an HTTP failure, `404` is a failure unless expected). Redirects are followed and the final status counts.
  `response_time_ms` is the time until response headers arrive. When the status passes, every assertion must hold on the response
  body (the first failure is recorded as `assertion`). The body is read only when there are assertions, at most 1 MiB, and never stored.
  Credentials (`auth_secret`) are only sent to the monitored host: they are dropped on a redirect to any other host.
- **Status transitions:** configurable `FAILURE_THRESHOLD` (default 2) consecutive failures confirm an outage; `RECOVERY_THRESHOLD`
  (default 2) consecutive successes confirm recovery; `unknown → up` on the first success. `Store.Record` returns the `Transition`
  (`WentDown` / `Recovered`) that Phase 5 will turn into incidents and notifications inside the same transaction.
- **Incidents and alerts (Phase 5):** `Store.Record` calls `incidents.Engine` inside its transaction. `WentDown` opens an incident (partial unique
  index: one open per monitor) and enqueues one `notification_deliveries` row per enabled, verified recipient; any move into `up` closes an
  open incident and enqueues recovery deliveries. Status, incident and deliveries therefore commit atomically
  (`TestIncidentAndNotificationsCommitAtomicallyWithTheResult`). The dispatcher (in `cmd/worker`) sends them; see `docs/api.md` for retry rules.
- **Reporting (Phase 6):** reports read `check_results` directly (index `(monitor_id, checked_at)`); series are aggregated in SQL into at most 150
  buckets so dashboards never load raw history. At current scale (a 1-minute plan with 200 monitors writes ≈ 288k rows/day) this is fast enough; if
  org-wide 90–365-day charts become slow, add a pre-aggregated hourly rollup table fed by the retention job.
- **Retention policy** (`internal/retention`, hourly, `DISABLE_RETENTION` to turn off per instance; one pass at a time via a PostgreSQL advisory lock):
  check results and *resolved* incidents older than the plan's history window are deleted in batches of `RETENTION_BATCH` with a short pause between
  statements; live monitors and open incidents are never deleted (a downgrade only shrinks the history window); monitors the user deleted are purged
  after 30 days; expired sessions and spent/expired one-time tokens are removed. Deletion is irreversible — upgrading later does not bring history back.
  The window is resolved through `plans.EffectivePlan`, the same function that governs every other entitlement.
- **Charts:** Recharts, lazy-loaded with the dashboard/monitor pages (main bundle ≈ 294 kB). Design rules applied: single-series line (2px) with a
  10 % area wash, hairline solid horizontal grid, no legend for one series, hover/keyboard readout, a table view of every value, held-frame dimming on
  refetch, honest gaps, and status shown by icon + text rather than colour alone.
- **Billing (Phase 7):** `internal/billing`. Webhooks are verified on the raw body, stored in `billing_webhook_events` (UNIQUE provider+event id), acknowledged, and processed
  inline once and then by the worker with retry/backoff. A subscription event only identifies *which* subscription changed; the applied state is **re-read from Stripe**
  (Stripe does not guarantee order and says not to use `created` for ordering). `apply` locks the organization's subscription row, maps the status (`docs/billing.md`),
  resolves the plan from the configured price IDs and never lets a second subscription overwrite the first. Entitlements still come from `plans.EffectivePlan`: no code path
  grants a plan except a successfully applied provider state.
- **Brand assets:** `web/src/assets/brand/` holds the cleaned artwork from the supplied pack; `scripts/prepare_brand_assets.py` reproduces it
  (see `docs/branding.md`).
- **Not yet:** multi-region verification (the single threshold rule is the extension point), retention of old results (Phase 6).

## Decisions and assumptions

- **Module name `hawkeye`**, matching the repository directory.
- **Versions:** Go 1.26.9, Echo v4.16, pgx v5, golang-migrate v4, React 19, Vite 8. Echo v4 is used because it is the current stable major available.
- **Data access:** pgx with explicit SQL in the service packages. `sqlc` was considered for Phase 2 and dropped: the queries are few and the extra code generator added more machinery than it saved. Revisit if query volume grows.
- **Migrations tool:** golang-migrate as a library, wrapped by `cmd/migrate`, so containers need no extra tooling.
- **Process layout:** the scheduler runs inside `cmd/worker` (separate package, `DISABLE_SCHEDULER=true` turns it off per instance); there is no `cmd/scheduler` binary.
- **Job queue:** PostgreSQL-backed (`FOR UPDATE SKIP LOCKED`), durable across restarts; no Redis.
- **Go dependency pins:** `prometheus/client_golang` v1.23.2 and `golang.org/x/{net,sys,text,sync}` are pinned because newer releases require Go 1.26; revisit when the toolchain image moves.
- **Auth:** server-side sessions in HTTP-only same-site cookies plus double-submit CSRF (see `docs/api.md`, `docs/security.md`).
- **Entitlements** (`internal/plans`): handlers never compare limits; they call the service. Methods take a `DB` so checks run
  inside the caller's transaction. The effective plan comes from the organization's subscription:

  | Subscription status | Entitlements |
  |---|---|
  | `active`, `trialing` | subscribed plan |
  | `past_due` | subscribed plan (grace while the provider retries payment) |
  | `canceled`, `incomplete`, unknown, or no subscription row | Free plan (fail closed) |

  A Checkout redirect or session creation never changes a subscription; only verified webhooks will (Phase 7).
- **Concurrency:** monitor creation locks the organization row (`SELECT … FOR UPDATE`) inside its transaction, so parallel requests
  cannot pass the limit check together. `TestConcurrentCreatesNeverExceedLimit` fails (6–10 created instead of 5) if the lock is removed.
- **Downgrades:** nothing is deleted. The stored check interval is kept; the scheduler uses `max(stored, plan minimum)`.
- **Soft delete:** deleted monitors stop counting toward limits; their rows remain until retention (Phase 6) removes them.
- **Organization selection:** monitor endpoints use `X-Organization-ID` (verified) or the oldest membership. Personal organizations are the only kind today.
- **Go version:** Go 1.26.9 everywhere (go.mod, Docker, CI); earlier 1.25 pins were removed when govulncheck flagged stdlib issues.
- **Migrations:** 000001 installs `set_updated_at()`; 000002 adds users, organizations, members, plans (seeded), sessions and the two token tables; 000007 adds `billing_webhook_events` and `subscriptions.last_synced_at`; 000006 adds indexes for retention; 000005 adds `incidents`, `notification_settings` (+ confirmation tokens) and `notification_deliveries`; 000004 adds `check_results`, `check_jobs` and `monitors.next_check_at`; 000003 adds subscriptions (every organization starts `active` on Free, created at registration) and monitors.
- **Tests:** integration tests create a throwaway database per test (`testutil.NewDatabase`), so packages run in parallel safely.

## Planned phases

2 Auth & tenancy · 3 Monitors & plans · 4 Scheduler/workers & SSRF-safe checks · 5 Incidents & notifications ·
6 Dashboard & retention · 7 Stripe billing · 8 Production readiness (all complete).

## API checks

Monitors can send any of GET/HEAD/POST/PUT/PATCH/DELETE/OPTIONS with custom headers, a body, Basic/Bearer/API-key auth and response assertions (`internal/checks/request.go`). Credentials are encrypted at rest (`internal/secrets`, `docs/security.md`); state-changing methods need a verified email and a 5-minute minimum interval.

## SSL certificate expiry

The checker watches the TLS handshake with the monitored host (`httptrace`, `internal/checks/checker.go`) and returns the leaf certificate's `NotAfter` in `Result.CertExpiresAt`; an expired certificate is also captured from the verification error. `Store.Record` saves it on the monitor and, inside the same transaction, asks `incidents.Engine.CertificateExpiring` to queue warnings when the certificate enters a new bucket (30/14/7/1 days, 0 = expired; `checks.SSLBuckets`). Deliveries reuse the alert queue (`notification_deliveries`, migration `000011`), de-duplicated per monitor, recipient, certificate and bucket. The UI shows a countdown and timeline (`web/src/components/Certificate.tsx`).

## Phase 8 additions

Production Dockerfiles and Compose (`deploy/`, `docs/deployment.md`); SMTP sender; shared PostgreSQL rate limiter; API metrics on a private port (`:9091`) and alert rules; optional Sentry error tracking; account export/deletion/change-password; legal placeholder pages; backup/restore scripts (`docs/backup-restore.md`); egress firewall; smoke test (`scripts/smoke.sh`); security review (`docs/security-review.md`).
