# Architecture

## Approach

A modular monolith: one Go module, one PostgreSQL database, and separately runnable processes that share
`internal/` packages. No microservices, no Kubernetes.

## Current state (Phases 1–8)

```
cmd/api        REST API process (Echo)
cmd/migrate    applies embedded SQL migrations
cmd/oauthclient registers OAuth clients for the MCP server (Claude's connector)
cmd/loadtest   measures what the engine and history queries cost at a given check interval, on a scratch database (docs/plan-sub-minute-checks.md)
internal/auth          register/login/sessions/password reset/email verification (Argon2id, hashed tokens); social.go: sign-in with Google and GitHub (linking, confirming, sign-in methods)
internal/socialauth    the Google and GitHub OAuth clients (PKCE): the address to send the browser to, and the code turned into an identity
internal/organizations tenant access; every query scoped by membership; removing members and leaving
internal/invitations   team invitations: an emailed, single-address link that adds a member (seats are held by pending invitations)
internal/oauth         OAuth 2.1 for the MCP server: registered clients (cmd/oauthclient), consent, codes with PKCE, rotating refresh tokens, revocation; access tokens are API tokens of their own kind
internal/mcpserver     the MCP server (POST /mcp, mounted in internal/httpapi/mcp.go): 18 tools over the same services as the REST handlers, acting as the API key's person with the same checks
internal/twofactor     two-factor sign-in: TOTP (RFC 6238, codes used once), sealed secrets, hashed recovery codes, and an owner's requirement for their organization (7-day grace per member)
internal/adconversions sign-ups, checkouts and purchases reported to OpenAI Ads' Conversions API from the server (with consent), next to the browser pixel; sent by the worker
internal/attribution   how a new account found HawkEye (campaign tags, ?ref=, kind of ad click, linking site, landing page), cleaned and stored at sign-up
internal/audit         the security log: append-only events (who, with which key or app, from where), an owner's organization log and each person's account log, 365 days
internal/apitokens     API keys: create (shown once, hashed), list, revoke, authenticate as a person in one organization with their current role; emails before and when keys expire (worker)
internal/plans         plan catalog + the entitlement service (all limit decisions live here)
internal/monitors      monitor CRUD, validation, soft delete, per-organization scoping
cmd/worker             monitoring engine process: scheduler + check workers + metrics endpoint (:9090)
internal/safehttp      SSRF-safe HTTP client (see docs/security.md)
internal/checks        runs one check (`Check` for websites, `CheckDNS` for DNS records, `RunWorkflow` for multi-step API checks, in workflow.go: values passed between steps, redaction), classifies errors, applies results to monitor state (`Apply`, `Store.Record`)
internal/queue         durable PostgreSQL job queue (claim / lease / complete / release)
internal/scheduler     finds due monitors, enqueues jobs, jitter, backpressure
internal/worker        bounded-concurrency polling loops, metrics sampler
internal/metrics       Prometheus metrics
internal/incidents     incident engine (opened/resolved inside the result transaction), incident queries, and incident updates (notes posted on an incident, shown on status pages; migration `000032`)
internal/heartbeats    heartbeat (cron job) monitors: records pings from /ping/<token> as check results (`checks.Store.RecordTx`), and a sweeper in the worker records missed deadlines; never schedules a check
internal/statuspage public status pages (`/status/<slug>`; migration `000014`, several per organization up to `plans.max_status_pages` since `000022`; 30 s cache; exposes only owner-chosen monitor names, states, daily uptime and incident times), internal/maintenance windows (rule in SQL: `in_maintenance`, migration `000013`; `checks.Store.Record` skips state changes and incidents while one applies), internal/notifications recipients + confirmation, enqueue (idempotent), email rendering, webhook/Slack channels (`channels.go`: sealed URLs, signing, SSRF-safe delivery; migration `000012`), retrying dispatcher
internal/reports       range resolution (plan-clamped), check-based uptime, bucketed series, check history, dashboard summary, monthly report data
internal/reportpdf     draws a monthly uptime report as a PDF (plain Go, standard fonts, its own charts)
internal/branding      an organization's report branding (name and logo on PDF reports) and the monthly report email setting
internal/monthlyreport emails owners last month's PDF reports on the 1st, when they asked for it (worker)
internal/weeklysummary the Monday summary email (uptime, incidents, slowest sites, expiring certificates and domains) (worker)
internal/lifecycle     onboarding emails that nudge new accounts toward their first monitor (worker)
internal/badges        public uptime badges (SVG, opt-in per monitor, addressed by a random token)
internal/incidentdraft AI-drafted incident updates with Claude, opt-in per organization on paid plans; never posts anything itself
internal/waitlist      "Get notified" sign-ups for coming-soon features
internal/retention     batched per-plan data retention + credential housekeeping (runs inside cmd/worker)
internal/rollups       hourly totals of check results (check_rollups) built by a job beside the scheduler, and rollups.Source, which readers use to combine them with raw results
internal/billing       Stripe: provider interface (+ SDK implementation and fake), checkout/portal/cancel/resync, signed webhook intake, event processing
internal/email         Sender interface (console, in-memory and SMTP senders)
internal/account       data export, account deletion, change password
internal/contact       the public contact form (triage topics, support email, acknowledgment)
internal/domains       domain registration expiry: RDAP + WHOIS lookup, warnings, per-plan limits (see Domain expiry below)
internal/regions       the configured check locations (CHECK_REGIONS)
internal/statusbeat    the primary worker's heartbeat to HawkEye's own status page (status/, a Cloudflare Worker that checks HawkEye from outside)
internal/ratelimit     shared PostgreSQL fixed-window rate limiter
internal/canary        tells our own network trouble apart from a customer's site being down
internal/errtrack      optional Sentry error tracking (scrubbed)
internal/healthcheck   container health probes
internal/secrets       AES-256-GCM sealing of stored credentials and webhook URLs
internal/publichost    reads a typed host name into a public one (no IPs, credentials or odd ports), for the free tools and DNS monitors
internal/toolguard     abuse limits shared by the free checkers: result cache, concurrency cap, per-host budget, deadline
internal/sslcheck      the SSL certificate checker: reads and verifies the certificate a host presents on port 443
internal/sitecheck     the website status checker: one GET / of a public host, with status, timing and verdict
internal/discovery     "set up from your domain": proposes monitors (home page, health endpoint, api subdomain, MX) from outside
internal/validation    field-level validation error
internal/testutil      per-test isolated, migrated PostgreSQL databases
internal/e2e           one acceptance test of the whole system (API, engine, incidents, alerts, billing webhooks, account deletion)
internal/config    env loading + validation (fails fast, no production fallbacks)
internal/logging   slog (JSON in production, text otherwise)
internal/database  pgx pool + golang-migrate
internal/httpapi   Echo wiring: request ID, logging, recover, body limit, timeout, error envelope, /healthz, /readyz
migrations/    versioned SQL, embedded via go:embed
web/           React + TypeScript (Vite, Vitest): the public site (landing, /monitoring campaign page, free tools, legal pages, status pages), sign-in, and the app (dashboard, monitors, incidents, alerts, domains, maintenance, billing, account)
```

- `/healthz` is liveness only. `/readyz` pings PostgreSQL (2s timeout) and returns 503 without internal details on failure.
- All errors use `{"error": {"code", "message", "details"}}`; unhandled errors are logged server-side and returned as `INTERNAL_ERROR`.

## Monitoring engine (Phase 4)

Readers of history (uptime, status pages, charts, monthly and weekly reports) don't add up `check_results` directly: `internal/rollups` keeps hourly
totals per monitor, and `rollups.Source` combines them with raw results for the partial hours at either end of a range and anything newer than
`rollup_state.through`, so answers are the same as adding up raw results (an equivalence test checks every reader). Only the check history list and
incidents read raw results. Why: `docs/plan-sub-minute-checks.md`.

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
- **Immediate re-check (migration `000051`, `docs/plan-immediate-recheck.md`):** a first failure of a healthy location on a healthy monitor sets
  `Outcome.Recheck` (`checks.wantRecheck`): the worker then calls `queue.Recheck`, which puts the *same* job row back with `run_at = now() +
  RECHECK_DELAY` (default 15 s), `attempts = 0` and `recheck = true`, instead of deleting it. Reusing the row keeps `UNIQUE (monitor_id, region)`,
  adds nothing to queue depth, and the scheduler skips the region meanwhile; if the worker dies first, the lease expiry runs it as an ordinary check.
  The re-check's result counts toward the streaks as usual (so it confirms or clears the failure), but is stored with `check_results.recheck` and left
  out of uptime, response times, rollups and slow-response streaks. Per monitor, `monitors.recheck` is `auto` (only when every request is GET, HEAD
  or OPTIONS, including a workflow's steps; DNS lookups qualify), `always` or `off`; heartbeats never re-check. No re-check while the monitor or the
  location is down, from a re-check, during maintenance, or when the next scheduled check is within twice the delay (30-second checks).
- **Timing breakdown (migration `000052`, `docs/plan-timing-breakdown.md`):** `safehttp` resolves host names and dials checked addresses
  itself, so `httptrace` never sees DNS or connect: `safehttp.WithPhases` puts a `Phases` accumulator on the request context, `dialContext` adds
  the DNS lookup and connect time (including failed attempts on other addresses), and `checks.run` adds the TLS handshake and the server's wait
  (`WroteRequest` to `GotFirstResponseByte`) from `httptrace`. Every phase is summed over all hops of a redirect chain; a phase still in progress
  when the request fails counts up to the failure. Stored as `smallint` columns on `check_results` (NULL without a breakdown) and as sums plus
  `phase_count` in `check_rollups`, so `rollups.Source` gives averages from raw results and totals alike. `incidents.SlowStarted` compares the
  confirming check with the monitor's week before its slow streak (`usualTimings`) and stores the explanation in `slowdowns.cause`.
- **Incidents and alerts (Phase 5):** `Store.Record` calls `incidents.Engine` inside its transaction. `WentDown` opens an incident (partial unique
  index: one open per monitor) and enqueues one `notification_deliveries` row per enabled, verified recipient; any move into `up` closes an
  open incident and enqueues recovery deliveries. Status, incident and deliveries therefore commit atomically
  (`TestIncidentAndNotificationsCommitAtomicallyWithTheResult`). The dispatcher (in `cmd/worker`) sends them; see `docs/api.md` for retry rules.
- **Reporting (Phase 6):** reports add up hourly totals (`check_rollups`, `internal/rollups`) for whole hours and `check_results` (index
  `(monitor_id, checked_at)`) for the rest; series are aggregated in SQL into at most 150 buckets so dashboards never load raw history. Adding up
  raw results alone took seconds for 90 days of 50 monitors (a 1-minute plan with 200 monitors writes ≈ 288k rows/day), which the totals brought
  to milliseconds (`docs/plan-sub-minute-checks.md`).
- **Retention policy** (`internal/retention`, hourly, `DISABLE_RETENTION` to turn off per instance; one pass at a time via a PostgreSQL advisory lock):
  check results and *resolved* incidents older than the plan's history window are deleted in batches of `RETENTION_BATCH` with a short pause between
  statements; live monitors and open incidents are never deleted (a downgrade only shrinks the history window); monitors the user deleted are purged
  after 30 days; expired sessions and spent/expired one-time tokens are removed. Deletion is irreversible — upgrading later does not bring history back.
  The window is resolved through `plans.EffectivePlan`, the same function that governs every other entitlement.
- **Charts:** Recharts, lazy-loaded with the dashboard/monitor pages (main bundle ≈ 294 kB). Design rules applied: single-series line (2px) with a
  10 % area wash, hairline solid horizontal grid, no legend for one series, hover/keyboard readout, a table view of every value, held-frame dimming on
  refetch, honest gaps, and status shown by icon + text rather than color alone.
- **Billing (Phase 7):** `internal/billing`. Webhooks are verified on the raw body, stored in `billing_webhook_events` (UNIQUE provider+event id), acknowledged, and processed
  inline once and then by the worker with retry/backoff. A subscription event only identifies *which* subscription changed; the applied state is **re-read from Stripe**
  (Stripe does not guarantee order and says not to use `created` for ordering). `apply` locks the organization's subscription row, maps the status (`docs/billing.md`),
  resolves the plan from the configured price IDs and never lets a second subscription overwrite the first. Entitlements still come from `plans.EffectivePlan`: no code path
  grants a plan except a successfully applied provider state.
- **Brand assets:** `web/src/assets/brand/` holds the finished artwork (logo, icons, background art)
  (see `docs/branding.md`).
- **DNS change alerts (migration `000054`, `docs/plan-dns-change-alerts.md`):** `checks.CheckDNS` returns the normalized answers on
  `Result.DNSAnswers` (never stored with the result). `Store.RecordTx` folds them, in the result's transaction, into the location's
  `dns_baselines` row and `dns_seen` pool (`checks.FoldDNS`, pure and unit-tested: baseline, 2-check confirmation, the A/AAAA 7-day pool) and,
  once a quorum of the monitor's locations has a confirmed change, calls `Incidents.DNSChanged`; `DNSChangeReverted` when none has. `incidents`
  stores the change (`dns_changes`, one open per monitor) and queues `dns_change` / `dns_change_closed` like slowdowns; accepting
  (`incidents.AcceptDNSChange`) resets every location's baseline under the monitor's row lock.
- **Deploys (`internal/deploys`, migration `000053`, `docs/plan-deploy-maintenance.md`):** a deploy is a `maintenance_windows` row of
  `kind = 'deploy'` starting now; `ended_at` ends it early (`maintenance_window_active` honors it), so alerts and uptime treat it exactly like
  maintenance, while `in_announced_maintenance` keeps it off public status pages. The check after a deploy runs in the API process through
  `internal/runcheck` (shared with the worker, so a monitor is checked the same way) and the same SSRF-safe checker, within the request's time limit,
  and isn't recorded. Deploy keys (`api_tokens.scope = 'deploy'`) are limited to `deployRoutes` in `internal/httpapi/tokens.go`.
- **Multi-region checks (`internal/regions`, migration `000015`):** a monitor lists the locations it runs in (`monitors.regions`, empty = primary only), capped by `plans.max_regions`. The scheduler queues one job per monitor *and* region (`UNIQUE (monitor_id, region)`; backpressure per region); a worker started with `WORKER_REGION` claims only its region's jobs and labels results (`check_results.region`). `Store.Record` keeps one streak per region (`monitor_region_state`) and decides the monitor's state in the same transaction: down while a majority of its effective regions are down (`regions.Quorum`), recovered when that majority is lost. A monitor with a single effective region takes the original code path unchanged. Results from a region the monitor no longer uses, and from maintenance windows, are recorded but change nothing. Remote workers need private access to PostgreSQL, and run with `WORKER_CHECKS_ONLY=true`, which leaves the scheduler, alert delivery, billing and scheduled emails to the primary (see "Regional worker hosts" in `docs/deployment.md`).

## Decisions and assumptions

- **Module name `hawkeye`**, matching the repository directory.
- **Versions:** Go 1.26.11, Echo v5, pgx v5, golang-migrate v4, React 19, Vite 8 (exact versions in `go.mod` and `web/package.json`). Echo moved from v4 to v5
  because the Go vulnerability database listed `GO-2026-6872` and `GO-2026-6875` against every v4 release; v5 refuses a cross-site request marked
  `Sec-Fetch-Site: same-site` even with a valid CSRF token (`docs/security-review.md`).
- **Data access:** pgx with explicit SQL in the service packages. `sqlc` was considered for Phase 2 and dropped: the queries are few and the extra code generator added more machinery than it saved. Revisit if query volume grows.
- **Migrations tool:** golang-migrate as a library, wrapped by `cmd/migrate`, so containers need no extra tooling.
- **Process layout:** the scheduler runs inside `cmd/worker` (separate package, `DISABLE_SCHEDULER=true` turns it off per instance); there is no `cmd/scheduler` binary.
- **Job queue:** PostgreSQL-backed (`FOR UPDATE SKIP LOCKED`), durable across restarts; no Redis.
- **Go toolchain:** Go 1.26.11 in `go.mod`, which the build images (`deploy/Dockerfile.api`, `deploy/Dockerfile.worker`) and CI follow. The earlier pins of
  `prometheus/client_golang` and `golang.org/x/*` that waited for Go 1.26 no longer apply.
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
- **Go version:** Go 1.26.11 everywhere (go.mod, Docker, CI); earlier pins were raised whenever govulncheck flagged a stdlib issue (1.26.11 for GO-2026-6859).
- **Migrations:** 000001 installs `set_updated_at()`; 000002 adds users, organizations, members, plans (seeded), sessions and the two token tables; 000007 adds `billing_webhook_events` and `subscriptions.last_synced_at`; 000006 adds indexes for retention; 000005 adds `incidents`, `notification_settings` (+ confirmation tokens) and `notification_deliveries`; 000004 adds `check_results`, `check_jobs` and `monitors.next_check_at`; 000003 adds subscriptions (every organization starts `active` on Free, created at registration) and monitors.
- **Tests:** integration tests create a throwaway database per test (`testutil.NewDatabase`), so packages run in parallel safely.

## Planned phases

2 Auth & tenancy · 3 Monitors & plans · 4 Scheduler/workers & SSRF-safe checks · 5 Incidents & notifications ·
6 Dashboard & retention · 7 Stripe billing · 8 Production readiness (all complete).

## API checks

Monitors can send any of GET/HEAD/POST/PUT/PATCH/DELETE/OPTIONS with custom headers, a body, Basic/Bearer/API-key auth and response assertions (`internal/checks/request.go`). Credentials are encrypted at rest (`internal/secrets`, `docs/security.md`); state-changing methods need a verified email and a 5-minute minimum interval. JSON checks compare values (`equals`, `contains`, ...) or numbers (`greater_than`, `less_or_equal`, ...). **Send test request** in the form (`components/TestRequest.tsx`, `POST /monitors/test-request`, `checks.Checker.TryRequest`) runs the unsaved request once and returns the first 256 KB of the body with every check's outcome (`checks.EvaluateEach`); the form shows a JSON body as a list of fields (`web/src/jsonRules.ts`), and clicking one offers checks for it. The body is never stored. Multi-step checks' Run test does the same per step (`checks.Checker.TryWorkflow`, `components/StepResponses.tsx`), with secrets and saved values hidden in each body, and a clicked field can also become a saved value for later steps. Intervals run from 30 s (Agency only; heartbeats and multi-step checks 60 s; new monitors default to 60 s) to a day, and a timeout is at most half the interval (`monitors.validateTiming`).

**Paste a cURL command** fills in the API check form, or a workflow step, from a cURL command (`web/src/curl.ts`, `components/CurlImport.tsx`). It is browser-only: no endpoint reads commands, and the result is saved through the normal monitor endpoints, so the server's validation is the only rule that counts. The parser leaves out the headers the server reserves; `web/src/reservedHeaders.ts` mirrors `reservedHeaders` in `internal/monitors/monitors.go`, and `TestReservedHeadersMatchTheWebApp` fails if the two differ. **Import from OpenAPI or Postman** (`/monitors/import`, `pages/OpenApiImportPage.tsx`) reads an OpenAPI 3.x or Swagger 2.0 file with `web/src/openapi.ts`, or a Postman v2.x collection with `web/src/postman.ts` (variables filled in, auth inherited from folders and the collection, saved examples setting the expected status), in a Web Worker (`openapi.worker.ts`, stopped after 20 s) so a large file can't freeze the tab. It follows `$ref`s inside the file only, and turns each operation into a `POST /monitors` body; the chosen ones are created one by one like *Set up from a domain*. The page is lazy-loaded because it brings the `yaml` parser. Design and decisions: `docs/plan-api-import.md`.

## SSL certificate expiry

The checker watches the TLS handshake with the monitored host (`httptrace`, `internal/checks/checker.go`) and returns the leaf certificate's `NotAfter` in `Result.CertExpiresAt`; an expired certificate is also captured from the verification error. `Store.Record` saves it on the monitor and, inside the same transaction, asks `incidents.Engine.CertificateExpiring` to queue warnings when the certificate enters a new bucket (30/14/7/1 days, 0 = expired; `checks.SSLBuckets`). Deliveries reuse the alert queue (`notification_deliveries`, migration `000011`), de-duplicated per monitor, recipient, certificate and bucket. The UI shows a countdown and timeline (`web/src/components/Certificate.tsx`).

## Domain expiry

`internal/domains`. A domain is a registrable name (`Registrable`, public suffix list) tracked per organization (`domains` table, migration `000016`). `SyncTx` keeps the table in step with the organization's monitors: `monitors.Service` calls it (through `WithDomainSync`) inside the transaction of every create, URL change and delete, under the organization row lock, and the worker's job reconciles every organization once a day (which also backfills monitors that predate the feature). Domains added by hand (`manual`) are never dropped by it.

The worker runs `Job` (advisory-locked, so one pass at a time across workers): every `DOMAIN_CHECK_INTERVAL` it finds domains whose `next_check_at` has come, **within the plan's `max_domains`** (the oldest by `created_at`; the rank is computed, not stored, so a plan change takes effect at once), and looks each up with `Lookup`: RDAP first (`rdap.go`: IANA bootstrap cached for a day, `domain/<name>` query, `expiration` event), then WHOIS (`whois.go`) where RDAP has no service or no expiry date. Outcomes: *known* (expiry stored; bucket logic as for certificates, in the same transaction as the queued warnings), *unavailable* (registry gives no date; reason stored; rechecked daily) or a *transient failure* (state untouched, `failure_count` and `last_error` recorded, exponential retry up to a day). `UnavailableError.Final` (the registry says the name does not exist) stops the fallback so a missing name is not asked about twice.

Warnings reuse `notification_deliveries` (`event_type = 'domain_expiry'`, `domain_id`, `domain_expires_at`, `domain_bucket`; the `notification_deliveries_shape` constraint says exactly which columns each event type carries) and the dispatcher, with email, webhook and chat renderers in `internal/notifications` (`chatContent` builds one channel-neutral message that Slack, Discord, Teams and Telegram each render and escape for their own markup; migration `000020` widens the channel list). Slow-response alerts (migration `000021`) run in the same result transaction: `checks.ApplySpeed` keeps slow/normal streaks (on the monitor, or per location in `monitor_region_state` with the same majority rule as outages), and `Incidents.SlowStarted`/`SlowEnded` open and resolve a row in `slowdowns` and queue `slow`/`slow_recovery` deliveries keyed by `slowdown_id`. Slowdowns are deliberately separate from incidents, so slowness never counts as downtime. UI: `web/src/pages/DomainsPage.tsx`, `web/src/components/Domains.tsx`.

## Status pages on customers' own domains

`internal/statuspagedomains` (migrations `000044`, `000045`; design and decisions in `docs/plan-status-page-domains.md`). A status page (Agency,
`plans.max_status_page_domains`) may claim one subdomain: `status_page_domains` holds the host name, the claim's TXT token, its state (`pending`,
`active`, `failing`, `disabled`) and the schedule. `Verifier` looks up `_hawkeye.<name>` TXT and where the name routes; `Next` (state.go, pure) turns
a result into the next state and check time. The primary worker runs `Job` (advisory-locked) every minute for due claims and emails the owner on a
change; the API runs the same check for "Check now".

Serving: Caddy's catch-all site (`STATUS_DOMAINS_ADDRESS`, `https://` in production) asks `GET /internal/tls/allow` before getting a certificate
(on-demand TLS), then serves `web/status.html` (entry `web/src/status-main.tsx`, which renders `StatusView` without the app) and proxies only
`/api/v1/public/domain-status[/logo]`, where `Service.Serving` maps the `Host` header to a page's slug (cached 30 s, and only while the plan includes
custom domains). `statuspage.Public` carries `custom_domain_url` and `redirect_to_domain`, so `/status/<slug>` can name the domain as canonical and
send visitors there.

## Phase 8 additions

Production Dockerfiles and Compose (`deploy/`, `docs/deployment.md`); SMTP sender; shared PostgreSQL rate limiter; API metrics on a private port (`:9091`) and alert rules; optional Sentry error tracking; account export/deletion/change-password; legal placeholder pages; backup/restore scripts (`docs/backup-restore.md`); egress firewall; smoke test (`scripts/smoke.sh`); security review (`docs/security-review.md`).

## Page titles, prerendering and the sitemap

`web/src/seo.ts` is the page map: every route's title, description and whether search engines may index it. Three things read it:

- **In the browser**, `usePageMeta` (called once in `GuardedRoutes`) sets the title, description and canonical link, or a robots `noindex`, on every navigation. Status pages and the 404 page are not in the map; they set their own title.
- **At build time**, `web/build/prerender-plugin.ts` writes one copy of the built `index.html` per page with a fixed address to `dist/pages/<name>.html` (home is `pages/home.html`), with that page's tags in the HTML, so crawlers and link previews that do not run JavaScript see them. It also writes `dist/sitemap.xml` from the pages marked `index: true`.
- **The web server** (`deploy/Caddyfile`) serves `/pages<path>.html` when it exists and the plain `index.html` otherwise (monitor pages with an id, status pages, 404s). `index.html` stays neutral (no canonical or robots tag) because it is that fallback.

To add a page: add the route in `App.tsx`, the path to the `@app` pattern in the Caddyfile, and an entry in `seo.ts`. `seo.test.ts` and `routes.test.ts` fail if one is missing. To make it appear in search results and the sitemap, set `index: true` (and make sure `robots.txt` does not block it). The tags are written at build time, so the page body is still rendered in the browser.

**Link-preview images** (`web/public/share/*.jpg`, 1200x630) come from `scripts/prepare_share_images.py` (text typeset in headless Chrome with the site's fonts, over a text-free background that is kept in git history, not in the repository: see `docs/branding.md`). A page names its image in `seo.ts` (`image`); pages without one use `/share/home.jpg`. `seo.test.ts` fails if a named image is missing.

## Free tools (`/tools`)

Public pages that rank in search and lead to sign-up; each ends with a call to action (`ToolShell`) and records `tool_use`, `tool_result` (the outcome only, never the domain) and `tool_cta_click`.

| Page | What it does | Where the work happens |
|---|---|---|
| `/tools/uptime-sla-calculator` | allowed downtime for an uptime percentage; uptime delivered for a downtime | browser (`web/src/tools/sla.ts`) |
| `/tools/downtime-cost-calculator` | revenue at risk from an outage | browser (`web/src/tools/downtimeCost.ts`) |
| `/tools/ssl-certificate-checker` | certificate expiry, issuer, trust, problems | `POST /api/v1/tools/ssl-check`, `internal/sslcheck` |
| `/tools/is-website-down` | does a site answer: verdict, status code, response time, final address | `POST /api/v1/tools/site-check`, `internal/sitecheck` |

The two checkers are the only public endpoints that connect to a host a visitor names. They share the destination policy of the monitoring engine (`internal/safehttp`), input parsing (`internal/publichost`) and abuse limits (`internal/toolguard`); the endpoints sit behind per-IP limits and CSRF like every POST. The API container makes these connections (see `docs/security-review.md`, item 12, for what that does and does not protect). `DISABLE_FREE_TOOLS=true` switches both off (503); `docs/operations.md#free-tools-abuse` is the runbook. Live checks against real hosts: `SSLCHECK_LIVE=1` and `SITECHECK_LIVE=1` (run on Linux).

Setting up from a domain (`/monitors/setup`, `POST /api/v1/monitors/suggestions`, MCP `suggest_monitors`; `internal/discovery`) is the signed-in
counterpart: it uses the same client, parsing and limits to propose monitors, and the app creates the chosen ones through `POST /monitors`. It is not
a free tool, so `DISABLE_FREE_TOOLS` leaves it on.

## Settings and secrets (`docs/secrets.md`)

Every setting a server needs is in one Infisical environment (`prod`). A server holds only `~/.hawkeye-infisical.env` (a read-only machine identity). `scripts/sync-secrets.sh` runs `scripts/fetch-secrets.py`, which logs in (`POST /api/v1/auth/universal-auth/login`), lists the secrets (`GET /api/v4/secrets`), validates them against `deploy/env.manifest` and writes `.env.production` (the containers' environment) and `~/.hawkeye-host.env` (backups and heartbeat only). `scripts/deploy.sh` does this on every release, keeps `.env.production.previous` so a rollback restores the settings together with the code, and has a `secrets-only` mode; `scripts/rebuild.sh` gives new servers the credentials file and the server fetches the rest. Without a credentials file everything falls back to the existing `.env.production`, so the secret manager can be adopted step by step. The fetch and validation run against a fake Infisical in `scripts/test_*.py` (CI job `scripts`).
