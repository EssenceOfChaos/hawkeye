# Architecture

## Approach

A modular monolith: one Go module, one PostgreSQL database, and separately runnable processes that share
`internal/` packages. No microservices, no Kubernetes.

## Current state (Phases 1–5)

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
internal/notifications recipients + confirmation, enqueue (idempotent), email rendering, retrying dispatcher
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
  `response_time_ms` is the time until response headers arrive.
- **Status transitions:** configurable `FAILURE_THRESHOLD` (default 2) consecutive failures confirm an outage; `RECOVERY_THRESHOLD`
  (default 2) consecutive successes confirm recovery; `unknown → up` on the first success. `Store.Record` returns the `Transition`
  (`WentDown` / `Recovered`) that Phase 5 will turn into incidents and notifications inside the same transaction.
- **Incidents and alerts (Phase 5):** `Store.Record` calls `incidents.Engine` inside its transaction. `WentDown` opens an incident (partial unique
  index: one open per monitor) and enqueues one `notification_deliveries` row per enabled, verified recipient; any move into `up` closes an
  open incident and enqueues recovery deliveries. Status, incident and deliveries therefore commit atomically
  (`TestIncidentAndNotificationsCommitAtomicallyWithTheResult`). The dispatcher (in `cmd/worker`) sends them; see `docs/api.md` for retry rules.
- **Brand assets:** `web/src/assets/brand/` holds the cleaned artwork from the supplied pack; `scripts/prepare_brand_assets.py` reproduces it
  (see `docs/branding.md`).
- **Not yet:** multi-region verification (the single threshold rule is the extension point), retention of old results (Phase 6).

## Decisions and assumptions

- **Module name `hawkeye`**, matching the repository directory.
- **Versions:** Go 1.25, Echo v4.16, pgx v5, golang-migrate v4, React 19, Vite 8. Echo v4 is used because it is the current stable major available.
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
- **Go version:** `golang.org/x/time` is pinned to v0.15.0 because v0.16 requires Go 1.26 and the Docker image uses Go 1.25.
- **Migrations:** 000001 installs `set_updated_at()`; 000002 adds users, organizations, members, plans (seeded), sessions and the two token tables; 000005 adds `incidents`, `notification_settings` (+ confirmation tokens) and `notification_deliveries`; 000004 adds `check_results`, `check_jobs` and `monitors.next_check_at`; 000003 adds subscriptions (every organization starts `active` on Free, created at registration) and monitors.
- **Tests:** integration tests create a throwaway database per test (`testutil.NewDatabase`), so packages run in parallel safely.

## Planned phases

2 Auth & tenancy · 3 Monitors & plans · 4 Scheduler/workers & SSRF-safe checks · 5 Incidents & notifications ·
6 Dashboard & retention · 7 Stripe billing · 8 Production readiness.
