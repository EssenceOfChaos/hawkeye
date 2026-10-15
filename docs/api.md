# API (Phases 2–8)

Base path `/api/v1`. JSON only. Operational probes `/healthz` and `/readyz` are outside the prefix.

## Errors

```json
{"error": {"code": "VALIDATION_ERROR", "message": "…", "details": {"fields": {"email": "…"}}}}
```

| Status | Code | Meaning |
|---|---|---|
| 400 | `VALIDATION_ERROR` / `INVALID_JSON` / `INVALID_TOKEN` | bad input; bad or expired verification/reset token |
| 401 | `UNAUTHENTICATED` / `INVALID_CREDENTIALS` | no/expired session; wrong email or password |
| 403 | `CSRF_INVALID` / `FORBIDDEN` / `EMAIL_NOT_VERIFIED` | missing CSRF header; insufficient role; unverified email |
| 403 | `PLAN_LIMIT_REACHED` | a plan limit blocks the operation (see below) |
| 404 | `NOT_FOUND` | unknown resource **or one in another tenant** |
| 409 | `EMAIL_TAKEN` | registration with an existing email |
| 429 | `RATE_LIMITED` | a rate limit was hit (per-IP, per-account login ceiling, or a global per-IP ceiling); sends `Retry-After` seconds |
| 5xx | `INTERNAL_ERROR` | details are logged server-side only |

Unknown paths under `/api/v1/` return 404 `NOT_FOUND` for everyone, signed in or not.

## Authentication model

Browser sessions use an opaque random token in the HTTP-only, `SameSite=Lax` cookie `hawkeye_session`
(`Secure` in production). Only its SHA-256 hash is stored. Default lifetime 14 days (`SESSION_TTL`), fixed (not sliding).
Logout deletes the session; a password reset deletes all of the user's sessions.

**CSRF:** every unsafe request (POST/PATCH/DELETE) must send header `X-CSRF-Token` equal to the `csrf_token` cookie.
The cookie is set on the first `/api/v1` response, so the SPA should call `GET /auth/me` on load.

## Endpoints

| Method & path | Auth | Notes |
|---|---|---|
| `POST /auth/register` | – | `{email, password}` → 201 `{user}`; creates a personal organization (owner), sends a verification email, starts a session. Password 10–128 chars. |
| `POST /auth/login` | – | `{email, password}` → `{user}` |
| `POST /auth/logout` | – | 204; idempotent |
| `POST /auth/forgot-password` | – | `{email}` → always 202 (no account enumeration). Newest link only; valid 1h. |
| `POST /auth/reset-password` | – | `{token, password}` → 204; revokes all sessions; also verifies the email |
| `POST /auth/verify-email` | – | `{token}` → 204; links valid 48h, single use |
| `POST /auth/resend-verification` | session | 202; invalidates older links |
| `POST /auth/change-password` | session | `{current_password, new_password}` → 204; signs out other sessions, emails a notice |
| `GET /account/export` | session | the caller's own data as a JSON download |
| `POST /account/delete` | session | `{password, confirm_email}` (typed email must match) → 204; cancels the Stripe subscription, deletes sole-member organizations; 409 `OWNS_SHARED_ORGANIZATION`; wrong password 403 `WRONG_PASSWORD` while the user owns an organization with other members |
| `GET /auth/me` | session | `{user, organizations[]}` |
| `GET /organizations` | session | organizations the caller belongs to, with their `role` |
| `GET /organizations/:id` | session | 404 if not a member |
| `PATCH /organizations/:id` | session, owner | `{name}` (1–100 chars); members get 403 |
| `GET /organizations/:id/members` | session | any member may list |

## Tenant-scoped endpoints

These act on one organization: the one named by header `X-Organization-ID` (membership is verified; a foreign or
malformed ID gives 404) or, if absent, the caller's oldest organization. Any member may manage monitors.

| Method & path | Notes |
|---|---|
| `GET /plans` | public price list (active plans, cheapest first) |
| `GET /usage` | `{usage: {plan, monitors_used, monitor_limit, over_limit, next_plan}}` |
| `GET /monitors` | filters `status` (unknown/up/down), `enabled`, `q` (name/URL substring); pagination `limit` (1–100, default 25) and `offset`; returns `{monitors, total, limit, offset}`, newest first |
| `POST /monitors` | `{name, url, method?, expected_status_code?, check_interval_seconds?, timeout_seconds?, headers?, body?, auth?, assertions?}` → 201 |
| `GET/PATCH/DELETE /monitors/:id` | PATCH is partial; `expected_status_code: null` and `body: null` clear them, `headers` and `assertions` replace the whole list. DELETE is a soft delete (204) and frees a plan slot |
| `POST /monitors/:id/pause`, `/resume` | toggles `enabled`; allowed even when over the plan limit |

**Monitor fields.** Editable: `name` (1–100), `url`, `method` (`GET` default, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`), `expected_status_code`
(100–599 or null; null means any response below 400 is up), `check_interval_seconds` (60–86400, default = plan minimum),
`timeout_seconds` (1–30, default 10). Read-only and ignored if sent: `current_status`, `consecutive_*`, `last_*`,
`enabled` (use pause/resume), `organization_id`. `effective_check_interval_seconds` is what the scheduler will use: the stored
interval raised to the plan minimum, so a downgrade takes effect without rewriting data.

**API check fields.**

- `headers`: up to 20 `{name, value}` pairs (values up to 1024 characters, no line breaks). Names the checker controls are refused:
  `Host`, `Content-Length`, `Transfer-Encoding`, `Connection`, `User-Agent`, `Accept-Encoding` and similar. `Authorization` is refused
  too, because header values are returned by the API; use `auth`.
- `body`: up to 64 KB of text, only with `POST`, `PUT`, `PATCH` or `DELETE`. Sent as `application/json` when it is valid JSON,
  otherwise as `text/plain`, unless a `Content-Type` header is set.
- `auth`: `{type: "none"}`, `{type: "basic", username, secret}`, `{type: "bearer", secret}` or `{type: "header", header_name, secret}`.
  The secret is write-only: responses carry `{type, username?, header_name?, secret_set}`. On PATCH, omitting `secret` keeps the stored
  one as long as `type` is unchanged; `type: "none"` deletes it. Data exports never include it. It is stored encrypted (AES-256-GCM, bound to the monitor, key from
  `SECRETS_ENCRYPTION_KEY`); deleting a monitor erases its credentials, headers and body immediately.
- `assertions`: up to 10, all of which must pass after the status check. `{type: "body_contains" | "body_not_contains", value}`
  searches the first 1 MiB of the body. `{type: "json_path", path, operator, value?}` parses the body as JSON (failing above 1 MiB);
  `path` looks like `data.items[0].status` (a leading `$.` is optional); `operator` is `equals`, `not_equals`, `contains` (substring
  of a string, or an element of an array), `exists` or `not_exists`. Strings compare as text, numbers numerically, and other values
  by their JSON form (`true`, `null`). Not allowed with `HEAD`. A failed assertion is recorded with `error_category: "assertion"`.

**State-changing methods.** `POST`, `PUT`, `PATCH` and `DELETE` repeat a request that changes things on the target, so they need a **verified email address**
(otherwise 403 `EMAIL_NOT_VERIFIED`, when creating a monitor or switching an existing one to such a method) and a **check interval of at least 5 minutes**
(`check_interval_seconds` below 300 is a 400 on that field; the default for such a monitor is raised to 300 s). Read-only methods (`GET`, `HEAD`, `OPTIONS`) keep the plan's speed.

Validation errors for list items name the item, for example `headers[0].name` or `assertions[2].path`.

**URL rules.** `http`/`https` only, no credentials, ports 80/443 only, no `localhost`/`.local`/`.internal`/single-label hosts,
no literal private/loopback/link-local/metadata IPs. This is early feedback only; the real SSRF defence (DNS resolution and
dial-time validation) is in the checker (`internal/safehttp`). Editing the URL, method, headers, body or authentication resets health state to `unknown`.

## Incidents

An incident opens when a monitor's outage is confirmed (consecutive failures, `FAILURE_THRESHOLD`) and resolves when it has recovered
(`RECOVERY_THRESHOLD` consecutive successes). `started_at` is the time of the first failed check of the confirming streak and
`resolved_at` the time of the first successful check of the recovery streak. At most one incident is open per monitor (enforced by a
database index). Deleting a monitor closes its open incident silently.

| Method & path | Notes |
|---|---|
| `GET /incidents` | organization-wide, newest first. Query: `status` (`open`/`resolved`), `from`, `to` (RFC 3339, on `started_at`), `limit` (1–100, default 25), `offset` |
| `GET /monitors/:id/incidents` | same filters for one monitor; 404 if it belongs to another organization |

Incident: `{id, monitor_id, monitor_name, started_at, resolved_at, status, cause_category, summary, duration_seconds}`
(`duration_seconds` is the elapsed time so far while open).

## Reports, history and the dashboard

All report endpoints are tenant-scoped (a monitor from another organization is a 404) and **bounded**: no endpoint returns unbounded history.

**Time range.** Either `range=24h|7d|30d|90d|365d` (default `24h`) or an explicit `from` and `to` (RFC 3339, together, not combined with `range`).
`to` is capped at now, a window may not exceed 366 days, and history older than the plan's retention does not exist, so `from` is clamped to
the retention cutoff. Every response echoes the effective window: `range: {from, to, preset, clamped_to_retention, retention_days}`; when
`clamped_to_retention` is true the UI tells the user their plan keeps less history than they asked for.

| Method & path | Notes |
|---|---|
| `GET /monitors/:id/uptime` | `{range, uptime}` (see below) |
| `GET /monitors/:id/response-times` | `{range, bucket_seconds, points: [{t, avg_ms, max_ms, checks, failed}]}`. At most 150 points: the bucket width (1 min up to 3 days) is chosen from the window. Buckets with no checks are **omitted**, so charts show gaps rather than zeros. `avg_ms`/`max_ms` cover successful checks only and are null for a bucket that only had failures |
| `GET /monitors/:id/results` | check history, newest first: `{range, results: [{id, checked_at, success, http_status_code, response_time_ms, error_category, error_summary}], total, limit, offset}`; `limit` 1–200 (default 50) |
| `GET /dashboard/summary` | `{range, summary: {monitors: {total, up, down, pending, paused}, open_incidents, uptime, certificates, certificates_tracked, generated_at}}`. `certificates` lists up to five enabled HTTPS monitors whose certificate expires within 30 days or already has (`{monitor_id, monitor_name, expires_at, days_remaining, status: expiring|expired}`, soonest first); `certificates_tracked` counts monitors with a known expiry. Counts and open incidents are *current*; `uptime` follows the range. Paused wins over status in the counts; deleted monitors are excluded |
| `GET /dashboard/recent-incidents` | `limit` 1–20 (default 5) |
| `GET /dashboard/response-times` | same shape as the per-monitor series, averaged across all live monitors |

**Uptime** — `{from, to, clamped_to_retention, total_checks, successful_checks, failed_checks, uptime_percent, insufficient_data, min_checks, avg_response_ms, method: "check_based"}`

> uptime % = successful checks ÷ completed checks × 100, over the window

- It is *check-based*, not continuous: only completed checks are counted. Periods when a monitor was paused, or when a check was late, never ran or was
  cancelled by a shutdown, produce no result and are therefore outside the denominator.
- The percentage is **truncated** to three decimals, never rounded up, so it reads 100 only when every completed check succeeded.
- With fewer than 5 completed checks (`min_checks`), `uptime_percent` is `null` and `insufficient_data` is true.
- Organization-wide figures use only live (non-deleted) monitors. A failed check of any kind counts as a failure, including `blocked` and `timeout`.

## Plans and billing

`GET /plans` (public) → `{plans, billing_enabled}`. Billing details and the Stripe setup are in `docs/billing.md`.

| Method & path | Who | Notes |
|---|---|---|
| `GET /billing/subscription` | any member | `{subscription: {billing_enabled, plan, status, paid_access, cancel_at_period_end, current_period_end, can_manage, has_subscription}, usage}`. `plan` is the plan whose limits apply now; `status` is `active`, `trialing`, `past_due`, `canceled` or `incomplete` |
| `POST /billing/checkout-session` | owner, verified email | `{plan: "pro"\|"agency"}` → `{url}` (Stripe-hosted Checkout). Only the plan name is read; price and organization come from the server. Grants nothing. 409 `ALREADY_SUBSCRIBED`, 400 validation, 503 `BILLING_NOT_CONFIGURED` |
| `POST /billing/portal-session` | owner, verified email | → `{url}` (customer portal). 409 `NO_BILLING_ACCOUNT` before a first checkout |
| `POST /billing/cancel-subscription` | owner, verified email | schedules cancellation at period end; access continues until then. 409 `NO_ACTIVE_SUBSCRIPTION` |
| `POST /billing/sync` | owner | re-reads the subscription from the provider (repair after missed webhooks) |
| `POST /webhooks/stripe` | Stripe | raw JSON body + `Stripe-Signature`. 200 `{received, duplicate}`; 400 `INVALID_SIGNATURE`; 503 when webhooks are not configured. No session/CSRF |

Provider failures return 502 `BILLING_PROVIDER_ERROR` with a generic message.

## Alert recipients (notification settings)

Each owner's own address is a recipient from registration; it starts receiving alerts when the account email is verified.
Alerts go only to destinations that are **enabled and verified**.

| Method & path | Who | Notes |
|---|---|---|
| `GET /notification-settings` | any member | `{notification_settings: [{id, user_id, channel, destination, enabled, verified, created_at}], max}` |
| `POST /notification-settings` | owner, verified email | `{channel?, destination}`; `channel` is `email` (default), `slack` or `webhook`. **Email:** your own address is verified at once; any other address is emailed a confirmation link and gets no alerts until confirmed. **Slack / webhook:** `destination` is an `https://` URL (Slack: `https://hooks.slack.com/services/…`; no IP addresses, port 443 only). A test message is delivered first and nothing is saved unless the receiver accepts it (400 `VALIDATION_ERROR` naming the reason). The URL is stored encrypted and listings show only its host (`hooks.slack.com/…`). A webhook also returns `signing_secret` **once**. Rate-limited. 409 `DESTINATION_EXISTS`, 403 `NOTIFICATION_SETTINGS_LIMIT` (max 10) |
| `POST /notification-settings/:id/test` | owner, verified email | Sends a test message to a Slack or webhook destination → 204; 400 if it is not delivered or the destination is an email address |
| `PATCH /notification-settings/:id` | owner | `{enabled}` only; to change an address, delete and re-add |
| `DELETE /notification-settings/:id` | owner | 204 |
| `POST /notification-settings/:id/resend-confirmation` | owner, verified email | 202; older links stop working |
| `POST /notification-settings/confirm` | public | `{token}` from the confirmation email → 204; single use, valid 48 h |

## Check locations

| Method & path | Who | Notes |
|---|---|---|
| `GET /regions` | any member | `{regions: [{code, name}], primary, max_regions}`: the installation's locations and how many one monitor may use on the caller's plan (never more than exist). A single-location installation returns one region |
| `GET /monitors/:id/regions` | any member | `{regions: [{region, name, status, last_checked_at, last_error}]}`: the locations the monitor runs in now (in the order chosen), each with its own status (`unknown`, `up`, `down`) |

Monitors have `regions` (create and PATCH accept it; `[]` means the primary location only). An unknown or repeated code is a 400 `VALIDATION_ERROR`; more than the plan allows is 403 `PLAN_LIMIT_REACHED` with `limit_type: "regions"`. A monitor is down while a **majority** of its locations are down. Check results carry `region`.

## Status page

One public page per organization at `/status/<slug>`, available on every plan. Only the monitors the owner adds appear, under the name the owner chooses. **Monitor URLs, error messages and causes are never exposed.** Nothing is public until `published` is true; a draft and a page that does not exist both answer 404.

| Method & path | Who | Notes |
|---|---|---|
| `GET /public/status/:slug` | **no login** | `{title, description, overall, monitors: [{name, state, uptime_percent, days: [{date, uptime_percent, status}]}], incidents: [{monitor, started_at, resolved_at}], history_days, generated_at}`. `overall` is `operational`, `partial_outage`, `major_outage` or `maintenance`; `state` is `up`, `down`, `maintenance` or `unknown` (paused / no check yet); day `status` is `ok` (≥ 99.5 %), `degraded` (≥ 95 %), `down` or `none` (fewer than 5 checks). Uptime leaves out checks made during maintenance windows. `history_days` is 90, or the plan's retention if shorter (Free: 7). Incidents are those from the last 14 days. Cached for 30 s (`Cache-Control: public, max-age=30`); 120 requests/min/IP |
| `GET /status-page` | any member | `{status_page: {slug, title, description, published, monitors: [{monitor_id, display_name}]} \| null}` |
| `PUT /status-page` | owner | Creates or replaces the page (same body). `slug`: 3–40 lowercase letters, digits, hyphens. `display_name` empty = the monitor's own name. Publishing needs at least one monitor. 409 `SLUG_TAKEN`, 400 `VALIDATION_ERROR` (for example a monitor from another organization) |
| `DELETE /status-page` | owner | 204; the address becomes available again |

## Maintenance windows

Planned periods during which a monitor's checks keep running and are recorded (`in_maintenance: true` on the result), but its status, streaks and incidents do not change, no alert is sent, and the checks are left out of uptime. Health resumes from where it was when the window ends. An incident that is already open stays open; certificate-expiry warnings are still sent. Monitors carry `in_maintenance` (true while a window covers them). **Paid plans only** (`plans.maintenance_enabled`: Pro and Agency); a Free organization gets 403 `PLAN_LIMIT_REACHED` on create, and can still list and delete.

| Method & path | Who | Notes |
|---|---|---|
| `GET /maintenance-windows` | any member | `{maintenance_windows: [{id, name, starts_at, duration_minutes, repeat, all_monitors, monitor_ids, status, next_start, created_at}], max}`. `status` is `active`, `upcoming` or `ended` (a finished one-off); `next_start` is the next occurrence, `null` once a one-off is over |
| `POST /maintenance-windows` | owner | `{name, starts_at, duration_minutes, repeat?, all_monitors?, monitor_ids?}` → 201. `starts_at` is an RFC 3339 instant (a start in the past begins now, but a one-off that has already ended is refused). `duration_minutes` 1–10080; for `repeat` `daily`/`weekly` it must be shorter than the period. Repeats are exact multiples of 24 h / 7 d after `starts_at` (UTC; they do not follow daylight saving). Either `all_monitors: true` (includes monitors added later) or a non-empty `monitor_ids` of your own monitors. 403 `MAINTENANCE_WINDOWS_LIMIT` (max 50) |
| `DELETE /maintenance-windows/:id` | owner | 204 |

To change a window, delete it and create a new one.

### Webhook and Slack alerts

Every enabled, verified destination receives outage, recovery and certificate-expiry alerts through the same durable queue as email (retries with backoff, at-least-once). A 2xx is success; 408, 429, 5xx and network errors are retried; any other 4xx (for example 404/410 for a revoked URL) fails the delivery permanently. Requests go through the SSRF-safe client, so private addresses are refused.

Webhook requests are `POST` with `Content-Type: application/json` and these headers: `X-HawkEye-Event` (`outage`, `recovery`, `ssl_expiry`, `test`), `X-HawkEye-Delivery` (stable across retries; use it to drop duplicates), `X-HawkEye-Timestamp` (Unix seconds) and `X-HawkEye-Signature` = `sha256=` + hex HMAC-SHA256 of `"<timestamp>.<raw body>"` keyed with the signing secret. Reject requests whose signature does not match or whose timestamp is old. Body:

```json
{"event": "outage", "sent_at": "2026-10-12T15:00:00Z", "url": "https://app.example.com/incidents",
 "monitor": {"id": "…", "name": "Site", "url": "https://example.com"},
 "incident": {"started_at": "…", "resolved_at": null, "cause": "Received HTTP 503"}}
```

`ssl_expiry` carries `certificate: {expires_at, warning_days}` instead of `incident` (`warning_days` 30, 14, 7, 1, or 0 once expired); `test` carries neither. Slack destinations receive an equivalent message in Slack's incoming-webhook format (unsigned).

## SSL certificate expiry

Every monitor object carries `ssl`: `null` until a check has seen the certificate (and always for `http://` monitors), otherwise
`{expires_at, checked_at, days_remaining, status}` with `status` `valid` (more than 30 days left), `expiring` (30 or fewer) or `expired`;
`days_remaining` is whole days and negative once expired. It is read on **every check** from the TLS handshake with the monitored host itself (never from a host the
request is redirected to), and it is also recorded when the handshake failed *because the site's certificate had expired*. Changing a monitor's URL clears it. Available on every plan.

**Warning emails** go to the organization's enabled, verified alert recipients when the certificate first falls inside a bucket: **30, 14, 7 and 1 day(s)** before expiry, and once more
when it has **expired** (bucket 0). They use the same delivery queue as outage alerts (`event_type = 'ssl_expiry'`, retries, idempotency). Each (monitor, recipient, certificate expiry date, bucket) is sent at most once;
a renewed certificate (new expiry date) restarts the schedule. A recipient added after a bucket was handled does not receive a backlog. Paused and deleted monitors send nothing.

## Notification delivery

One logical notification exists per (incident, recipient, event) — or, for certificate warnings, per (monitor, recipient, certificate expiry, bucket) — — a unique constraint — created in the same transaction as the incident,
so it cannot be lost or duplicated. The worker's dispatcher claims due deliveries with a visibility timeout and sends them; transient
failures retry with backoff 1 m, 5 m, 15 m, 1 h, 3 h up to `NOTIFY_MAX_ATTEMPTS` (default 6), then the delivery is marked `failed`.
Permanent errors (`email.ErrPermanent`) fail immediately. Delivery is at-least-once: a crash between sending and recording can cause one duplicate email.
Settings disabled or removed after queuing are skipped.

## Contact form

| Method & path | Auth | Notes |
|---|---|---|
| `GET /contact/categories` | – | the triage topics: `{categories: [{code, label, hint, priority}]}` |
| `POST /contact` | – (session optional) | `{category, name, email, subject, message, url?}` → 202 `{reference}`. Emails the support inbox with Reply-To = `email`; a signed-in sender's account id and plan are added by the server. 400 `VALIDATION_ERROR` with `details.fields`; 429 when rate-limited; 503 `CONTACT_UNAVAILABLE` if delivery failed. A non-empty `company` field (spam trap) is answered 202 and silently dropped. |

## Plan limits

`403 PLAN_LIMIT_REACHED` with details:

```json
{"error": {"code": "PLAN_LIMIT_REACHED",
  "message": "Your Free plan includes up to 5 monitors and all of them are in use.",
  "details": {"limit_type": "monitors", "limit": 5, "used": 5, "plan": "free",
              "upgrade_url": "https://app.example.com/billing",
              "next_plan": {"code": "pro", "name": "Pro", "max_monitors": 50, "monthly_price_cents": 1900}}}}
```

`limit_type` is `monitors`, `check_interval` (limit = minimum seconds) or `team_members`. If an organization is *above* its limit
after a downgrade, existing monitors are untouched, creation is refused, the message says how many to delete, and `usage.over_limit` is true.

## Known trade-offs

- `EMAIL_TAKEN` reveals that an address is registered (better UX than a silent no-op). Registration is rate-limited; revisit if enumeration matters.
- Rate limits are in-memory and per API process; with several replicas the effective limit multiplies. A shared store is a Phase 8 item.
- Results, uptime and incident endpoints arrive with the checker (Phases 4–6).
