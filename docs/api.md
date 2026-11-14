# API (Phases 2–8)

Base path `/api/v1`. JSON only. Operational probes `/healthz` and `/readyz` are outside the prefix and answer both GET and HEAD.

## Errors

```json
{"error": {"code": "VALIDATION_ERROR", "message": "…", "details": {"fields": {"email": "…"}}}}
```

| Status | Code | Meaning |
|---|---|---|
| 400 | `VALIDATION_ERROR` / `INVALID_JSON` / `INVALID_TOKEN` | bad input; bad or expired verification/reset token |
| 401 | `UNAUTHENTICATED` / `INVALID_CREDENTIALS` | no/expired session, or a wrong, revoked or malformed API key; wrong email or password |
| 401 | `TOKEN_EXPIRED` | an API key past its expiry date |
| 403 | `CSRF_INVALID` / `FORBIDDEN` / `EMAIL_NOT_VERIFIED` | missing CSRF header; insufficient role; unverified email |
| 403 | `SESSION_REQUIRED` / `TOKEN_SCOPE` / `TOKEN_ORGANIZATION_MISMATCH` | an API key on a route not open to keys; a read-only key on a write; `X-Organization-ID` names another organization than the key's |
| 403 | `API_KEYS_LIMIT` | the organization already has 25 live API keys |
| 503 | `API_KEYS_DISABLED` | API keys are turned off on this server (`DISABLE_API_KEYS`) |
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

### Two-factor sign-in

Optional for each person (`internal/twofactor`, migration `000034`). With it on, `POST /auth/login` answers a correct password with
`200 {two_factor_required: true, challenge}` and **no session**; `POST /auth/login/two-factor {challenge, code}` then checks a code from the
authenticator app (or a recovery code) and starts the session (`{user, used_recovery_code}`). A challenge lasts 10 minutes and allows 5 wrong codes
(`401 INVALID_CODE` with `details.tries_left`, then `401 SIGN_IN_EXPIRED`: start again with the password). Each authenticator code works once.

| Method & path | Auth | Notes |
|---|---|---|
| `GET /account/two-factor` | session | `{two_factor: {enabled, enabled_at, recovery_codes_left}}` |
| `POST /account/two-factor/setup` | session | `{password}` → `{setup: {secret, otpauth_uri, qr_code}}` (a PNG data URL). Starting again replaces the pending secret |
| `POST /account/two-factor/enable` | session | `{code}` from the app → `{recovery_codes: [10]}`, shown once. Emails the person |
| `POST /account/two-factor/recovery-codes` | session | `{code}` (app or recovery code) → 10 new codes; the old ones stop working |
| `POST /account/two-factor/disable` | session | `{password, code}` → 204. `409 TWO_FACTOR_REQUIRED_BY_ORGANIZATION` for the owner of an organization that requires it |
| `GET /organizations/:id/two-factor` | session, owner | `{required, grace_days, members: [{user_id, email, two_factor_enabled, grace_ends_at?, blocked}]}` |
| `PUT /organizations/:id/two-factor` | session, owner, verified email | `{required}`. Off by default. Turning it on needs the owner to use it (`409 OWNER_NEEDS_TWO_FACTOR`) and emails members without it |

**Organization requirement.** When an owner requires it, each member without it has 7 days from the later of that moment and when they joined.
After that, every tenant-scoped request for that organization (session or API key, REST or MCP) answers `403 TWO_FACTOR_REQUIRED` until they set
it up; their account, `/auth/me`, `/account/*`, leaving and their other organizations keep working. `/auth/me` reports `two_factor` and, for each
organization, `require_two_factor`, `two_factor_grace_ends_at` and `two_factor_blocked`. Ownership can't be transferred to a member who would be
shut out (`409 NEW_OWNER_NEEDS_TWO_FACTOR`).

### API keys

Scripts and tools authenticate with an API key instead: `Authorization: Bearer hk_…` (`internal/apitokens`, migration `000033`). The public guide is
`/docs/api`; the design is in `docs/plan-api-mcp.md`.

- **What a key is.** `hk_` followed by 256 random bits. Only its SHA-256 hash is stored, with the first 10 characters (`prefix`) for display. It is
  returned once, by the request that creates it. A key belongs to **one organization and one person** and has a scope: `read` or `write`.
- **What it acts as.** The person, in the key's organization (`X-Organization-ID` may be left out; one naming another organization is refused), with
  the person's role **now**: owner-only routes still need the owner, and the role is read on every request. The key is deleted with the membership (a
  foreign key), so leaving or being removed takes a person's keys with them.
- **Writes.** A `read` key gets `403 TOKEN_SCOPE` on POST/PUT/PATCH/DELETE. A `write` key needs a plan with `api_write_enabled` (Pro, Agency),
  checked on every write: after a downgrade it gets `403 PLAN_LIMIT_REACHED` (`limit_type: api_write`) and keeps reading.
- **Where keys work.** Only on routes registered through `tokenGroup` in `registerAPI`: monitors and tags (including bulk actions), results, uptime,
  response times, incidents and incident updates, status pages, maintenance windows, domains, branding and reports, regions, the dashboard,
  `GET /usage`, `GET /billing/subscription` and `GET /notification-settings/coverage`. Everything else answers `403 SESSION_REQUIRED`: `/auth/*`,
  `/account/*`, `/organizations/*` (team, invitations, transfer), billing changes, alert recipients (including listing them) and `/api-keys`.
  `TestEveryRouteIsClassifiedForAPIKeys` lists every route and fails when a new one is not classified.
- **No CSRF, no cookie.** A request with an `Authorization` header authenticates with the key alone and never with a session cookie that came along,
  so it skips the CSRF check on routes open to keys (a browser never attaches that header on its own).
- **Expiry.** 90 days by default; a person may choose a date up to a year ahead, or no expiry. `401 TOKEN_EXPIRED` after it. The worker emails the
  person 7 days before (skipped for keys made to last 8 days or less) and when it expires (`apitokens.ExpiryJob`).
- **Revocation.** On the API keys page or `DELETE /api-keys/:id`; immediate. A password **reset** revokes all of the person's keys (someone who took
  over the account may have made them); changing the password while signed in does not.
- **Limits.** 25 live keys per organization. With the shared limiter, 120 requests and 30 writes a minute per key (`RATE_LIMITED`, `Retry-After`),
  on top of the per-IP limits. `last_used_at` is written at most once a minute.

| Method & path | Auth | Notes |
|---|---|---|
| `GET /api-keys` | session | `{keys: [{id, name, scope, prefix, owner_email, mine, created_at, last_used_at, expires_at, expired}], max_keys}`, newest first, revoked keys left out. The owner sees every key in the organization; a member sees their own |
| `POST /api-keys` | session, verified email | `{name, scope, expires_at?, no_expiry?}` → 201 `{key, token}`. Leave both expiry fields out for 90 days; `expires_at` (RFC 3339) must be 1 hour to 366 days away. A `write` key on a plan without API writes is `403 PLAN_LIMIT_REACHED`. Emails the person that a key was created |
| `DELETE /api-keys/:id` | session | 204. Your own keys; the owner may revoke anyone's in the organization. 404 for an unknown or already revoked key |

## OAuth (connecting apps to the MCP server)

MCP clients such as Claude can connect by signing in instead of with an API key (`internal/oauth`, migration `000035`, consent page `/connect`).
HawkEye is an OAuth 2.1 authorization server for exactly one resource, the MCP server, and supports only what MCP clients need.

| Endpoint | Purpose |
|---|---|
| `GET /.well-known/oauth-protected-resource` (also `…/mcp`) | RFC 9728 metadata: resource `<app>/mcp`, authorization server `<app>` |
| `GET /.well-known/oauth-authorization-server` | RFC 8414 metadata |
| `GET /oauth/authorize` | Checks the request and redirects to `/connect`. Unknown clients and unregistered redirect URIs are a 400 here, never a redirect; other problems are sent back to the client's redirect URI (`error=…`) |
| `POST /oauth/token` | Form-encoded. `authorization_code` (PKCE S256 required, `code_verifier`, the same `redirect_uri`) and `refresh_token`. Client authentication with HTTP Basic or `client_id`/`client_secret` in the form; public clients send `client_id` only |
| `POST /oauth/revoke` | RFC 7009: an access or refresh token ends its whole connection. 200 for unknown tokens |
| `GET /api/v1/oauth/authorize?<request>` | Session. What the consent page shows: the client's name, whether it asked for write access, the person's organizations (with `can_write` and `blocked`), and whether a two-factor code is needed |
| `POST /api/v1/oauth/authorize` | Session + CSRF. `{…request, organization_id, access: read|write, two_factor_code?}` → `{redirect}` (the client's redirect URI with `code`, `state`, `iss`). Emails the person |
| `GET /api/v1/oauth/connected-apps`, `DELETE /api/v1/oauth/connected-apps/:id` | Session. Live connections in the organization (the owner sees everyone's); disconnecting ends their tokens at once |

- **Clients** are registered by an operator with `cmd/oauthclient` (no dynamic registration). Redirect URIs match exactly.
- **Codes** last a minute and work once; a second use revokes what the first issued.
- **Access tokens** are `hk_` tokens of kind `oauth_access`, valid for an hour, accepted **only by `/mcp`** (the REST API answers
  `403 TOKEN_AUDIENCE`), never listed with API keys, and never sent expiry emails. **Refresh tokens** (`hkr_`) last 30 days and rotate;
  presenting a used one revokes the connection.
- **Scope** is `read` or `write`, chosen by the person on the consent page; `write` needs the client to ask for it and the organization's plan to
  include API writes. Connections follow the person's membership and two-factor requirement exactly like API keys.
- **`/mcp` 401s** carry `WWW-Authenticate: Bearer …, resource_metadata="<app>/.well-known/oauth-protected-resource/mcp"`.

## MCP server

`POST /mcp` (outside `/api/v1`) is a Model Context Protocol server for Claude Code and other MCP clients (`internal/mcpserver`, mounted in
`internal/httpapi/mcp.go`; public guide at `/docs/claude`). It takes **API keys only** (`Authorization: Bearer hk_…`), authenticated exactly as
the REST API does: the same errors (`401 UNAUTHENTICATED` / `TOKEN_EXPIRED`, `503 API_KEYS_DISABLED`), the same per-key request budget, and the
same kill switch. Requests with an `Origin` header must come from `https://claude.ai` or `https://claude.com` (`403 ORIGIN_NOT_ALLOWED`); clients
that are not browsers send none. It is stateless (no MCP sessions; `GET` and `DELETE` answer 405) and answers in JSON.

| Tool | Kind | Needs | Mirrors |
|---|---|---|---|
| `get_account_overview` | read | – | `GET /usage`, `GET /dashboard/summary` |
| `list_monitors`, `get_monitor` | read | – | `GET /monitors`, `GET /monitors/:id` (never headers, body or credentials) |
| `get_uptime` | read | – | `GET /monitors/:id/uptime` (or every monitor), presets only |
| `list_incidents`, `get_incident` | read | – | `GET /incidents`, `GET /incidents/:id/updates` |
| `list_status_pages`, `list_maintenance_windows` | read | – | `GET /status-pages`, `GET /maintenance-windows` |
| `suggest_monitors` | read, open world | – | `POST /monitors/suggestions`; returns `create_monitor` arguments, creates nothing |
| `pause_monitors`, `resume_monitors` | write | write key, paid plan | `POST /monitors/bulk` (by IDs or by tag name) |
| `create_monitor` | write | write key, paid plan | `POST /monitors` (website with GET, heartbeat or DNS only) |
| `post_incident_update`, `delete_incident_update` | write, destructive | write key, paid plan, owner | `POST`/`DELETE /incidents/:id/updates` |
| `schedule_maintenance` | write | write key, Pro or Agency, owner | `POST /maintenance-windows` (one-off) |
| `delete_maintenance_window` | write, destructive | write key, paid plan, owner | `DELETE /maintenance-windows/:id` |
| `create_workflow` | write, destructive, open world | write key, paid plan | `POST /monitors` with `type: "workflow"`, with secret *names* only (see below) |
| `test_workflow` | write, open world | write key, paid plan | `POST /monitors/test-workflow`, or a run of a saved workflow by `monitor_id` |

**Multi-step API checks over MCP.** Secret values never pass through an assistant. `create_workflow` takes the names of the secrets its steps use;
they are stored as declared but not entered (`monitor_secrets.value` NULL), the workflow is saved **paused**, and the result lists `secrets_to_enter` and
the `edit_url` where a person enters them. Until then resuming it (REST, MCP, bulk) is refused (`409 SECRETS_NOT_SET`). A step's credentials
(`auth_value`) must be a single placeholder (`{{token}}` or `{{secret.name}}`), never a literal. `create_workflow` is marked destructive, because its steps
call other services on every run, so Claude asks first; steps that write need a verified address. `test_workflow` runs a saved workflow (its stored
secrets) or a draft; a draft may not use secrets or POST, PUT, PATCH and DELETE, so an assistant can't make HawkEye change data elsewhere on demand. It
records no result and shares the app's 60 test runs an hour per organization. `get_monitor` lists a workflow's step names (`workflow_steps`).

Every tool has a title and `readOnlyHint`, or an explicit `destructiveHint`; destructive tools always ask the person first in Claude. A tool that is
refused returns a tool result with `isError` and a message that says how to fix it, never internal detail. Writes spend the key's write budget
(30 a minute), **shared with REST writes**. `TestToolsRefuseWhatTheAPIRefuses` runs every write tool next to its REST route for keys of each scope,
role and plan and fails if the two ever disagree.

## Endpoints

| Method & path | Auth | Notes |
|---|---|---|
| `POST /auth/register` | – | `{email, password, attribution?}` → 201 `{user}`; creates a personal organization (owner), sends a verification email, starts a session. Password 10–128 chars. `attribution` is how the browser found HawkEye (`{source, medium, campaign, click_id, referrer_host, landing_path, first_seen_at}`, all optional, sent by the web app while analytics is on); fields that are not short plain labels are dropped, never refused (`internal/attribution`). |
| `POST /auth/login` | – | `{email, password}` → `{user}` |
| `POST /auth/logout` | – | 204; idempotent |
| `POST /auth/forgot-password` | – | `{email}` → always 202 (no account enumeration). Newest link only; valid 1h. |
| `POST /auth/reset-password` | – | `{token, password}` → 204; revokes all sessions; also verifies the email |
| `POST /auth/verify-email` | – | `{token}` → 204; links valid 48h, single use |
| `POST /auth/resend-verification` | session | 202; invalidates older links |
| `POST /auth/change-password` | session | `{current_password, new_password}` → 204; signs out other sessions, emails a notice |
| `GET /account/email-preferences` | session | `{email_preferences: {weekly_summary, onboarding}}`: the optional emails the person receives (both on by default). Alerts, account and security email are not optional |
| `PUT /account/email-preferences` | session | `{weekly_summary?, onboarding?}` → the same; an omitted field is unchanged |
| `POST /lifecycle/unsubscribe?token=…&list=…` | – (no login, no CSRF) | The unsubscribe link in optional emails (and mail clients' one-click `List-Unsubscribe` POST). `list` is `onboarding` (the default) or `weekly_summary`; it stops only that list → 204, idempotent. 400 `INVALID_TOKEN` for an unknown token or list |
| `GET /account/export` | session | the caller's own data as a JSON download. For a team they belong to as a member, only its name and their role are included: its data is its owner's |
| `POST /account/delete` | session | `{password, confirm_email}` (typed email must match) → 204; cancels the Stripe subscription, deletes sole-member organizations; 409 `OWNS_SHARED_ORGANIZATION`; wrong password 403 `WRONG_PASSWORD` while the user owns an organization with other members |
| `GET /auth/me` | session | `{user, organizations[]}` |
| `GET /organizations` | session | organizations the caller belongs to, with their `role` |
| `GET /organizations/:id` | session | 404 if not a member |
| `PATCH /organizations/:id` | session, owner | `{name}` (1–100 chars); members get 403 |
| `GET /organizations/:id/members` | session | any member may list |
| `DELETE /organizations/:id/members/:userID` | session, owner | 204. Removes a member; they lose access on their next request and keep their own account. 409 `OWNER_PROTECTED` for the owner, 404 for someone who is not a member. See **Team** below |
| `POST /organizations/:id/leave` | session, member | 204. The caller leaves a team they were invited to. 409 `OWNER_PROTECTED` for an owner |

## Tenant-scoped endpoints

These act on one organization: the one named by header `X-Organization-ID` (membership is verified; a foreign or
malformed ID gives 404) or, if absent, the organization the caller **owns** (their own), else their oldest. Someone invited to a team therefore still opens their own organization by default, and sends `X-Organization-ID` to act for the team. Any member may manage monitors.

| Method & path | Notes |
|---|---|
| `GET /plans` | public price list (active plans, cheapest first) |
| `GET /usage` | `{usage: {plan, monitors_used, monitor_limit, over_limit, next_plan}}` |
| `GET /monitors` | filters `status` (unknown/up/down), `enabled`, `q` (name/URL substring), `tag` (a tag id); pagination `limit` (1–100, default 25) and `offset`; returns `{monitors, total, limit, offset}`, newest first |
| `POST /monitors` | `{name, url, method?, expected_status_code?, check_interval_seconds?, timeout_seconds?, headers?, body?, auth?, assertions?, slow_threshold_ms?, slow_checks?, tag_ids?}` → 201. For a heartbeat (cron job) monitor: `{type: "heartbeat", name, check_interval_seconds?, grace_seconds?, tag_ids?, alert_delay_minutes?, quiet_hours?}`; see *Heartbeat monitors*. For a DNS monitor: `{type: "dns", name, url, dns_record_type?, dns_expected?, check_interval_seconds?, timeout_seconds?, regions?, tag_ids?, alert_delay_minutes?, quiet_hours?}`; see *DNS monitors* |
| `GET/PATCH/DELETE /monitors/:id` | PATCH is partial; `expected_status_code: null`, `body: null` and `slow_threshold_ms: null` clear them, `headers`, `assertions` and `tag_ids` replace the whole list. DELETE is a soft delete (204) and frees a plan slot |
| `POST /monitors/:id/pause`, `/resume` | toggles `enabled`; allowed even when over the plan limit |
| `POST /monitors/suggestions` | `{domain}` → `{domain, suggestions: [{kind, reason, recommended, already_monitored, monitor: {type, name, url, dns_record_type?}}]}`. Creates nothing; see *Set up from a domain* |
| `POST /monitors/bulk` | `{monitor_ids, action, check_interval_seconds?, tag_ids?}` → `{updated}`. See *Tags and bulk actions* |
| `GET /monitors/:id/pings` | heartbeats only: the latest pings, newest first (`?limit=` 1–100, default 50) → `{pings: [{id, received_at, kind, exit_status, method, source_ip, user_agent, body, ignored}]}`; 404 for a website monitor |
| `POST /monitors/:id/ping-token` | heartbeats only: a new ping address → `{monitor}`; the old one stops working at once |
| `GET /tags` | `{tags: [{id, name, color, monitor_count}]}`, sorted by name |
| `POST /tags` | `{name, color?}` → 201 `{tag}` |
| `PATCH /tags/:id` | `{name?, color?}`; `color: null` clears it |
| `DELETE /tags/:id` | 204; the tag's monitors are kept |

**Monitor fields.** `type` is `http` (a website or API, the default), `heartbeat` or `dns`, set on create and fixed after that. Editable: `name` (1–100), `url`, `method` (`GET` default, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`), `expected_status_code`
(100–599 or null; null means any response below 400 is up), `check_interval_seconds` (60–86400, default = plan minimum),
`timeout_seconds` (1–30, default 10), `slow_threshold_ms` (100–60000 and below the timeout, or null = off; see *Slow-response alerts*) and `slow_checks` (2–10, default 3), `trace_context` (boolean, default false; see *Trace context*), `tag_ids`, `alert_delay_minutes` (0–1440, default 0) and `quiet_hours` (`{start, end, timezone}` or null; see *Alert routing, alert delay and quiet hours*). Read-only and ignored if sent: `slow_since`, `current_status`, `consecutive_*`, `last_*`,
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
no literal private/loopback/link-local/metadata IPs. This is early feedback only; the real SSRF defense (DNS resolution and
dial-time validation) is in the checker (`internal/safehttp`). Editing the URL, method, headers, body or authentication resets health state to `unknown`.

### Trace context

With `trace_context: true` (website and API monitors only; a 400 on `trace_context` for other types, or alongside a `traceparent` header of the
customer's own), every check sends a W3C Trace Context header, `traceparent: 00-<trace id>-<span id>-01`: a new random 128-bit trace ID and 64-bit span
ID per check, marked sampled, so the request appears in the site's own tracing (OpenTelemetry, Datadog, Honeycomb, Grafana and others) as the root of
its trace. The trace ID is kept with the result (`check_results.trace_id`) and returned as `trace_id` in the check history, so a failed check can be
looked up there. Off by default: for sites without tracing it changes nothing, and for sites with it, each check becomes a trace they may pay to keep.
Multi-step API checks don't send it yet. The header follows redirects like any other request header.

### Set up from a domain

`POST /monitors/suggestions` (`internal/discovery`) looks at a public site from HawkEye's servers and proposes monitors; the app shows them as a checklist
and creates the chosen ones with `POST /monitors`, so plan limits and validation are those of adding a monitor by hand. It is a POST so a link or
prefetch can't make HawkEye contact a site; with an API key it therefore needs a write key on a paid plan, like every POST. Over MCP,
`suggest_monitors` is a read tool and any key may use it. `kind` is, in this order:

- `website`: the home page, over https (or plain http when only that answers). `recommended` is false when it answered 4xx, which a monitor counts as
  down; a site that is down (5xx or no answer) is still recommended.
- `health`: the first of `/readyz`, `/healthz`, `/health`, `/api/health` that answers 2xx with a content type other than HTML. A site that answers a
  made-up path with 2xx (a single-page app, a catch-all route) is never credited with a health endpoint.
- `api`: `api.<registered domain>`, by the same health paths, or its root when that answers below 400. Left out when it does not exist or answer.
- `mail`: a DNS monitor on the registered domain's MX records, when it has any (a null MX is left out).

`already_monitored` is true when one of the organization's monitors checks the same URL (ignoring case and a trailing slash), or, for DNS, the same host
and record type. Only GET requests to those fixed paths are sent (User-Agent `HawkEye-Setup/1.0`), whatever path the input had, through the checker's
SSRF-safe client; no response body is kept or returned, and the domain is not stored. Limits: 10 a minute per IP and 30 an hour per organization (shared
with MCP), plus the free tools' per-host budget (5 fresh lookups per 10 minutes) and a 60 s cache. Errors are the free tools' (400 `INVALID_HOST`, 422
`HOST_NOT_FOUND` / `HOST_NOT_ALLOWED`, 429 `RATE_LIMITED` / `HOST_RATE_LIMITED`, 503 `BUSY`, 504 `HOST_TIMEOUT`).

### Tags and bulk actions

Tags group an organization's monitors, for example by client or project. Any member may manage them, on every plan. A tag `name` is 1–40
characters and unique in the organization regardless of case (409 `TAG_EXISTS`); `color` is `#rrggbb` or null. At most 100 tags per organization
(403 `TAGS_LIMIT`) and 10 per monitor. Monitors carry `tags: [{id, name, color}]`, sorted by name. `tag_ids` on create or PATCH must be the
organization's own tags (otherwise 400 on `tag_ids`). Deleting a monitor removes its tags; deleting a tag keeps its monitors. Data exports list
each monitor's tag names.

`POST /monitors/bulk` applies one `action` to up to 200 `monitor_ids`: `pause`, `resume`, `delete`, `set_interval` (with
`check_interval_seconds`), `add_tags` or `remove_tags` (with `tag_ids`), or `set_alert_timing` (with `alert_delay_minutes`, `quiet_hours`, or both: whichever is left out stays as it is on each monitor, and `quiet_hours: null` turns quiet hours off; validated like the single-monitor fields). It is **all or nothing**: if any listed monitor is missing, deleted or in
another organization the result is 404 and nothing changes, and if the action is not allowed for any of them (an interval below the plan's minimum,
403 `PLAN_LIMIT_REACHED`; below 5 minutes for a monitor using a state-changing method, or a monitor left with more than 10 tags, 400) nothing
changes either. `set_interval` changes website and DNS monitors only: a heartbeat's period follows its job's schedule, so heartbeats in the selection keep theirs. Repeated ids count once; `updated` is how many monitors were changed. The same rules as the single-monitor endpoints apply
(resume makes a monitor due at once; delete erases credentials, closes open incidents without alerts and frees plan slots).

## Team

An owner can invite colleagues to the organization, up to the plan's seats (`max_team_members`: Free and Pro 1, Agency 5). An invitation is an emailed link, valid for 7 days, bound to one address. Whoever follows it signs in (or creates an account) with **that** address, which must be **verified**, and joins as a `member`. A member can see and manage monitors and incidents like any member; owner-only actions (billing, alert recipients, status page, maintenance, domains, branding, and the team itself) stay with the owner. An organization has exactly one owner, who cannot be removed and cannot leave.

A pending invitation holds a seat, so an owner cannot send more invitations than there are seats. Accepting, canceling or letting one expire frees or uses the seat. If the plan shrinks (a downgrade) after an invitation was sent, nobody new can join until there is room; existing members are never removed automatically.

| Method & path | Who | Notes |
|---|---|---|
| `GET /organizations/:id/invitations` | owner | `{invitations: [{id, email, created_at, expires_at}]}`, pending only (expired ones are not listed) |
| `POST /organizations/:id/invitations` | owner, verified email | `{email}` → 201 `{invitation}`. Emails the link to that address. Sending to an address that is already invited replaces the link (the old one stops working) and needs no extra seat. 400 `VALIDATION_ERROR`, 409 `ALREADY_MEMBER`, 403 `PLAN_LIMIT_REACHED` (`limit_type: "team_members"`), 429 (per IP, and per target address per hour), 503 `INVITE_UNAVAILABLE` when the email could not be sent (nothing is saved) |
| `POST /organizations/:id/invitations/:invitationID/resend` | owner, verified email | 200 `{invitation}` with a new link and a fresh 7 days; the old link stops working |
| `DELETE /organizations/:id/invitations/:invitationID` | owner | 204. The link stops working at once and the seat is freed |
| `POST /invitations/preview` | – (no login) | `{token}` → `{invitation: {organization_name, email}}`, so the page can say what the link is for and which address to use. Changes nothing. 400 `INVALID_TOKEN` for an unknown, used, revoked or expired link |
| `POST /invitations/accept` | session | `{token, alerts?}` → `{organization, alerts?}` with the caller's role. With `alerts: true` the member's own (verified) address is added to the organization's alert recipients, linked to their membership; `alerts` in the reply is `added`, `already` (the owner had added that address: it is now confirmed and linked) or `full` (no room: they still join, and the owner is told). The owner is emailed whenever someone joins. 400 `INVALID_TOKEN`; 403 `INVITATION_EMAIL_MISMATCH` (signed in with a different address; the link is **not** used up); 403 `EMAIL_NOT_VERIFIED` (the account has not confirmed the address); 403 `PLAN_LIMIT_REACHED` (no seat left after a downgrade) |
| `POST /organizations/:id/transfer` | owner, verified email | `{user_id, confirm_name}` → 204. Makes a current member the owner and the caller a member. `confirm_name` must be the organization's name (case and surrounding spaces ignored), else 400 `VALIDATION_ERROR`; 404 for someone who is not a member; 409 `NEW_OWNER_NOT_VERIFIED` when the member has not confirmed their email address. Both people are emailed (the old owner with a "if this was not you" notice). The payment provider's customer email is changed to the new owner's, so receipts and invoices follow; if the provider cannot be reached the transfer still stands and the address can be changed in the billing portal. Rate-limited |

**Roles.** Every organization has exactly **one owner** (a unique index enforces it); ownership changes only by transfer. Members see everything and may add or change monitors; billing, alert recipients, status pages, domains, maintenance windows and the team are the owner's. A member may turn off or remove **their own** alert address (`PATCH`/`DELETE /notification-settings/:id` on the entry linked to their account; any other entry is a 404 for them). When a member is removed or leaves, their linked alert address is deleted with the membership, so they stop receiving the organization's alerts.

Only a SHA-256 hash of each link's token is stored. The link is the only thing that authorizes a preview; accepting needs the token, a session, the matching address, and a verified address, so a leaked link is useless to anyone who does not own the mailbox.

## Incidents

An incident opens when a monitor's outage is confirmed (consecutive failures, `FAILURE_THRESHOLD`) and resolves when it has recovered
(`RECOVERY_THRESHOLD` consecutive successes). `started_at` is the time of the first failed check of the confirming streak and
`resolved_at` the time of the first successful check of the recovery streak. At most one incident is open per monitor (enforced by a
database index). Deleting a monitor closes its open incident silently.

| Method & path | Notes |
|---|---|
| `GET /incidents` | organization-wide, newest first. Query: `status` (`open`/`resolved`), `from`, `to` (RFC 3339, on `started_at`), `limit` (1–100, default 25), `offset` |
| `GET /monitors/:id/incidents` | same filters for one monitor; 404 if it belongs to another organization |

Incident: `{id, monitor_id, monitor_name, started_at, resolved_at, status, cause_category, summary, duration_seconds, update_count}`
(`duration_seconds` is the elapsed time so far while open; `update_count` is how many updates have been posted on it).

### Incident updates

Notes the team posts on an incident ("Investigating", "Fixed"). They appear under the incident on every **published** status page that lists its monitor,
newest first, with their status, text and time, never who posted them. They are plain text: neither the API nor the page turns them into markup or links
(migration `000032`). Every plan includes them.

| Method & path | Who | Notes |
|---|---|---|
| `GET /incidents/:id/updates` | any member | `{updates: [{id, incident_id, status, message, author_email, created_at}], status_pages: [{title, slug}]}`, newest first. `status_pages` are the published pages that show these updates (empty: only the team sees them). `author_email` is null once that account is gone |
| `POST /incidents/:id/updates` | owner | `{status, message}` → 201 `{update}`. `status`: `investigating`, `identified`, `monitoring` or `resolved` (a label only: the incident still opens and resolves from the checks). `message`: 1–1,000 characters after trimming; `\r\n` becomes `\n`; control and text-direction characters are refused (400 `VALIDATION_ERROR` on `message`). At most 50 per incident. Works on open and resolved incidents. Rate-limited per IP |
| `DELETE /incidents/:id/updates/:updateID` | owner | 204 |

Another organization's incident, or a malformed id, is a 404. Posting or deleting clears the server's cache of public pages, so the next request shows the
change; a browser that already loaded a page may keep its copy for up to 30 seconds (`Cache-Control: max-age=30`). Updates go when their incident does
(retention, or deleting the monitor or the account), are in the account export, and stay without an author when that person's account is deleted.

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
| `GET /monitors/:id/results` | check history, newest first: `{range, results: [{id, checked_at, success, http_status_code, response_time_ms, error_category, error_summary, region, steps, trace_id}], total, limit, offset}`; `limit` 1–200 (default 50) |
| `GET /dashboard/summary` | `{range, summary: {monitors: {total, up, down, pending, paused}, open_incidents, uptime, certificates, certificates_tracked, domains, domains_tracked, generated_at}}`. `certificates` lists up to five enabled HTTPS monitors whose certificate expires within 30 days or already has (`{monitor_id, monitor_name, expires_at, days_remaining, status: expiring|expired}`, soonest first); `certificates_tracked` counts monitors with a known expiry. `domains` is the same for tracked domains (`{domain_id, name, expires_at, days_remaining, status}`); `domains_tracked` counts those within the plan's limit that have a known expiry. Counts and open incidents are *current*; `uptime` follows the range. Paused wins over status in the counts; deleted monitors are excluded |
| `GET /dashboard/recent-incidents` | `limit` 1–20 (default 5) |
| `GET /dashboard/response-times` | same shape as the per-monitor series, averaged across all live monitors |

**Uptime** — `{from, to, clamped_to_retention, total_checks, successful_checks, failed_checks, uptime_percent, insufficient_data, min_checks, avg_response_ms, method: "check_based"}`

> uptime % = successful checks ÷ completed checks × 100, over the window

- It is *check-based*, not continuous: only completed checks are counted. Periods when a monitor was paused, or when a check was late, never ran or was
  canceled by a shutdown, produce no result and are therefore outside the denominator.
- The percentage is **truncated** to three decimals, never rounded up, so it reads 100 only when every completed check succeeded.
- With fewer than 5 completed checks (`min_checks`), `uptime_percent` is `null` and `insufficient_data` is true.
- Organization-wide figures use only live (non-deleted) monitors. A failed check of any kind counts as a failure, including `blocked` and `timeout`.

## Plans and billing

`GET /plans` (public) → `{plans, billing_enabled, annual_billing_enabled}`. Each plan has `monthly_price_cents` and `annual_price_cents` (the price of a year paid at once,
or null when the plan is not sold yearly); `annual_billing_enabled` says whether yearly checkout is available on this server. Billing details and the Stripe setup are in `docs/billing.md`.

| Method & path | Who | Notes |
|---|---|---|
| `GET /billing/subscription` | any member | `{subscription: {billing_enabled, annual_billing_enabled, plan, status, paid_access, billing_interval, cancel_at_period_end, current_period_end, can_manage, has_subscription}, usage}`. `billing_interval` is `month` or `year` while the plan is paid for, else null. `plan` is the plan whose limits apply now; `status` is `active`, `trialing`, `past_due`, `canceled` or `incomplete` |
| `POST /billing/checkout-session` | owner, verified email | `{plan: "pro"\|"agency", interval?: "month"\|"year"}` → `{url}` (Stripe-hosted Checkout). Only the plan name and interval (default `month`) are read; price and organization come from the server. Grants nothing. 409 `ALREADY_SUBSCRIBED`, 400 validation (including `interval: "year"` while yearly billing is not configured), 503 `PLAN_UNAVAILABLE` while that price disagrees with the plans table, 503 `BILLING_NOT_CONFIGURED` |
| `POST /billing/portal-session` | owner, verified email | → `{url}` (customer portal). 409 `NO_BILLING_ACCOUNT` before a first checkout |
| `POST /billing/cancel-subscription` | owner, verified email | schedules cancellation at period end; access continues until then. 409 `NO_ACTIVE_SUBSCRIPTION` |
| `POST /billing/sync` | owner | re-reads the subscription from the provider (repair after missed webhooks) |
| `POST /webhooks/stripe` | Stripe | raw JSON body + `Stripe-Signature`. 200 `{received, duplicate}`; 400 `INVALID_SIGNATURE`; 503 when webhooks are not configured. No session/CSRF |

Provider failures return 502 `BILLING_PROVIDER_ERROR` with a generic message.

## Alert recipients (notification settings)

Each owner's own address is a recipient from registration; it starts receiving alerts when the account email is verified.
Alerts go only to destinations that are **enabled and verified**, and only for the monitors a destination is routed to (see *Alert routing* below).

| Method & path | Who | Notes |
|---|---|---|
| `GET /notification-settings` | any member | `{notification_settings: [{id, user_id, channel, destination, enabled, verified, monitor_scope, tag_ids, created_at}], max, telegram_bot}`; `telegram_bot` is the @name (without @) of HawkEye's bot when chats can be connected through it, else `""` |
| `POST /notification-settings` | owner, verified email | `{channel?, destination, bot_token?}`; `channel` is `email` (default), `slack`, `discord`, `teams`, `telegram` or `webhook`. **Email:** your own address is verified at once; any other address is emailed a confirmation link and gets no alerts until confirmed. **Slack / Discord / Teams / webhook:** `destination` is an `https://` URL (no IP addresses, port 443 only). Slack: `https://hooks.slack.com/services/…`; Discord: `https://discord.com/api/webhooks/<id>/<token>` (also `discordapp.com`, `ptb.`/`canary.discord.com`, `/api/v<n>/…`; a `?thread_id=` query is kept); Teams: a Workflows webhook (`https://….logic.azure.com/workflows/…` or `https://….powerplatform.com/…/workflows/…`; retired Office 365 connector URLs are refused). **Telegram (own bot):** `bot_token` is the token from @BotFather and `destination` the chat ID (a number such as `-1001234567890`, or `@channelname`); errors name `bot_token` or `destination`. To use HawkEye's bot instead, see `telegram-link` below. A test message is delivered first and nothing is saved unless the receiver accepts it (400 `VALIDATION_ERROR` naming the reason; for Telegram with a hint such as "check the chat ID"). The URL (for Telegram, the Bot API URL holding the token) is stored encrypted and listings show only its host (`hooks.slack.com/…`), or `chat <id>` for Telegram. A webhook also returns `signing_secret` **once**. Rate-limited. 409 `DESTINATION_EXISTS`, 403 `NOTIFICATION_SETTINGS_LIMIT` (max 10) |
| `POST /notification-settings/telegram-link` | owner, verified email | 201 `{telegram_link: {bot, code, chat_url, group_url, channel_url, expires_at}}`: a one-time code (15 minutes; a new link cancels the previous one) that connects a Telegram chat through HawkEye's bot. `chat_url` (`t.me/<bot>?start=<code>`) connects a private chat, `group_url` (`?startgroup=`) a group; for a channel, add the bot as an admin (`channel_url`) and post `/connect <code>` there. The chat appears in the list as a verified `telegram` destination named after the chat, once someone taps Start. 503 `TELEGRAM_UNAVAILABLE` when the bot is not configured or not reached yet; 403 `NOTIFICATION_SETTINGS_LIMIT` when the list is full |
| `POST /notification-settings/:id/test` | owner, verified email | Sends a test to one **confirmed** recipient → 204. **Email:** the "HawkEye test alert" email (gold, says nothing is wrong, names who asked); at most `EMAIL_ACTION_LIMIT_PER_HOUR` per address per hour, then 429 `RATE_LIMITED` with `Retry-After`. **Webhook / chat:** a test message (signed for webhooks); 400 if it is not delivered. An address that has not confirmed → 409 `NOT_VERIFIED` (nothing is sent). Works on a recipient that is turned off (it tests delivery, not the switch); another organization's id is a 404. |
| `PATCH /notification-settings/:id` | owner; a member for their own address | `{enabled}` only; to change an address, delete and re-add |
| `PUT /notification-settings/:id/routing` | owner | `{monitor_scope: "all"\|"tags", tag_ids}` → `{notification_setting}`. `all` (the default) is every monitor plus account-wide alerts; `tags` needs at least one of the organization's own tags (400 otherwise) |
| `GET /notification-settings/coverage` | any member | `{uncovered_monitors: [{id, name}], fallback_available}`: monitors no destination is routed to, and whether the fallback can reach anyone |
| `DELETE /notification-settings/:id` | owner; a member for their own address | 204 |
| `POST /notification-settings/:id/resend-confirmation` | owner, verified email | 202; older links stop working |
| `POST /notification-settings/confirm` | public | `{token}` from the confirmation email → 204; single use, valid 48 h |

### Alert routing, alert delay and quiet hours

**Routing.** A destination gets alerts for every monitor (`monitor_scope: "all"`, the default) or only for monitors carrying one of its tags (`"tags"`), so an
agency can send each client only that client's alerts. Account-wide alerts that belong to no monitor (domain expiry) go to the `all` destinations. When
routing would leave an alert with no destination, it goes instead to the **team's own email addresses** (email destinations that belong to a current
member), never to every destination: those can include other clients. With no such address the alert reaches nobody, which
`GET /notification-settings/coverage` (and a warning on the Notifications page) reports beforehand. Deleting a tag removes it from destinations; a
destination left with no tags receives nothing until it is changed.

**Alert delay** (`alert_delay_minutes` on a monitor, 0–1440, default 0): an outage alert is sent only once the outage has lasted that long, counted from its
first failed check. **Quiet hours** (`quiet_hours: {start: "HH:MM", end: "HH:MM", timezone}` or null; `end` before `start` crosses midnight; an IANA time
zone; start and end must differ): every day in that window the monitor's outage, recovery, slow-response and certificate alerts are held and sent when it
ends. Slow-response and certificate alerts ignore the delay. If the problem ends while its alert is still held, the alert is canceled
(`status: skipped`, `suppressed: ended_while_held`) and that destination gets no "recovered" or "back to normal" either. An alert that was never held but is
simply late (a stopped dispatcher) is never canceled. Held alerts keep the schedule they were given; changing the settings affects later alerts only.

## Check locations

| Method & path | Who | Notes |
|---|---|---|
| `GET /regions` | any member | `{regions: [{code, name}], primary, max_regions}`: the installation's locations and how many one monitor may use on the caller's plan (never more than exist). A single-location installation returns one region |
| `GET /monitors/:id/regions` | any member | `{regions: [{region, name, status, last_checked_at, last_error}]}`: the locations the monitor runs in now (in the order chosen), each with its own status (`unknown`, `up`, `down`) |

Monitors have `regions` (create and PATCH accept it; `[]` means the primary location only). An unknown or repeated code is a 400 `VALIDATION_ERROR`; more than the plan allows is 403 `PLAN_LIMIT_REACHED` with `limit_type: "regions"`. A monitor is down while a **majority** of its locations are down. Check results carry `region`.

## Status pages

Public pages at `/status/<slug>`. An organization may have as many as its plan includes (`plans.max_status_pages`: Free 1, Pro 3, Agency 50), so an agency can give each client its own; a monitor may appear on several pages. After a downgrade every existing page keeps working and can still be edited; only creating another is refused. Slugs are unique across the whole installation. Only the monitors the owner adds appear, under the name the owner chooses. **Monitor URLs, error messages and causes are never exposed.** Nothing is public until `published` is true; a draft and a page that does not exist both answer 404.

| Method & path | Who | Notes |
|---|---|---|
| `GET /public/status/:slug` | **no login** | `{branding, title, description, overall, monitors: [{name, state, uptime_percent, days: [{date, uptime_percent, status}]}], incidents: [{monitor, started_at, resolved_at, updates: [{status, message, posted_at}]}], history_days, generated_at}`. `overall` is `operational`, `partial_outage`, `major_outage` or `maintenance`; `state` is `up`, `down`, `maintenance` or `unknown` (paused / no check yet); day `status` is `ok` (≥ 99.5 %), `degraded` (≥ 95 %), `down` or `none` (fewer than 5 checks). Uptime leaves out checks made during maintenance windows. `history_days` is 90, or the plan's retention if shorter (Free: 7). Incidents are those from the last 14 days. Cached for 30 s (`Cache-Control: public, max-age=30`); 120 requests/min/IP |
| `GET /status-pages` | any member | `{status_pages: [{id, slug, title, description, published, monitors: [{monitor_id, display_name}], accent_color, website_url, hide_powered_by, has_logo, logo_version}], max}`, oldest first; `max` is the plan's limit |
| `POST /status-pages` | owner | `{slug, title, description?, published, monitors, accent_color?, website_url?, hide_powered_by?}` → 201 `{status_page}`. `slug`: 3–40 lowercase letters, digits, hyphens. `display_name` empty = the monitor's own name. Publishing needs at least one monitor. 409 `SLUG_TAKEN`, 400 `VALIDATION_ERROR` (for example a monitor from another organization), 403 `PLAN_LIMIT_REACHED` with `limit_type: "status_pages"` |
| `GET /status-pages/:id` | any member | `{status_page}`; another organization's page (or a malformed id) is a 404 |
| `PUT /status-pages/:id` | owner | Replaces the page (same body as POST) → 200. Allowed above the plan's limit |
| `DELETE /status-pages/:id` | owner | 204; the address becomes available again |
| `PUT /status-pages/:id/logo` | owner | The image itself as the body (PNG or JPEG, ≤ 512 KB, ≤ 2000 × 2000; never SVG); validated and re-encoded like report logos → 200 `{status_page}`. 400 `VALIDATION_ERROR` on `logo`; 403 `PLAN_LIMIT_REACHED` (`status_page_branding`) |
| `GET /status-pages/:id/logo` | any member | The stored logo for the editor's preview, whatever the plan; 404 when there is none |
| `DELETE /status-pages/:id/logo` | owner | → 200 `{status_page}`; allowed on every plan |
| `GET /public/status/:slug/logo` | **no login** | A published page's logo, only while its plan includes branding (else 404, so drafts and logos kept after a downgrade reveal nothing). `Cache-Control: public, max-age=3600` (the public page links to it with `?v=<version>`), `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'` |

**Branding.** `accent_color` is `#rrggbb` (stored lower-case) or `""`; the public header uses it as its background with black or white text, whichever reads better (always at least 4.5:1). `website_url` is an `https://` address (no credentials) or `""`, shown as "← Back to example.com". `hide_powered_by` removes "Powered by HawkEye". A logo, color and link need `plans.status_page_branding` (Pro, Agency); hiding "Powered by" needs `plans.status_page_white_label` (Agency); otherwise 403 `PLAN_LIMIT_REACHED` with `limit_type` `status_page_branding` or `status_page_white_label`. Only a *change* is checked: clearing is always allowed, and values stored before a downgrade may be saved again unchanged (they are kept but not shown). The public view carries `branding: {logo_url?, accent_color?, website_url?, powered_by}` with only what the plan currently allows.

## Maintenance windows

Planned periods during which a monitor's checks keep running and are recorded (`in_maintenance: true` on the result), but its status, streaks and incidents do not change, no alert is sent, and the checks are left out of uptime. Health resumes from where it was when the window ends. An incident that is already open stays open; certificate-expiry warnings are still sent. Monitors carry `in_maintenance` (true while a window covers them). **Paid plans only** (`plans.maintenance_enabled`: Pro and Agency); a Free organization gets 403 `PLAN_LIMIT_REACHED` on create, and can still list and delete.

| Method & path | Who | Notes |
|---|---|---|
| `GET /maintenance-windows` | any member | `{maintenance_windows: [{id, name, starts_at, duration_minutes, repeat, all_monitors, monitor_ids, status, next_start, created_at}], max}`. `status` is `active`, `upcoming` or `ended` (a finished one-off); `next_start` is the next occurrence, `null` once a one-off is over |
| `POST /maintenance-windows` | owner | `{name, starts_at, duration_minutes, repeat?, all_monitors?, monitor_ids?}` → 201. `starts_at` is an RFC 3339 instant (a start in the past begins now, but a one-off that has already ended is refused). `duration_minutes` 1–10080; for `repeat` `daily`/`weekly` it must be shorter than the period. Repeats are exact multiples of 24 h / 7 d after `starts_at` (UTC; they do not follow daylight saving). Either `all_monitors: true` (includes monitors added later) or a non-empty `monitor_ids` of your own monitors. 403 `MAINTENANCE_WINDOWS_LIMIT` (max 50) |
| `DELETE /maintenance-windows/:id` | owner | 204 |

To change a window, delete it and create a new one.

### HawkEye's Telegram bot

With `TELEGRAM_BOT_TOKEN` and `TELEGRAM_WEBHOOK_SECRET` set, the API learns the bot's name (`getMe`) and, when `API_BASE_URL` is `https://`, registers `POST /api/v1/webhooks/telegram` with Telegram (`setWebhook` with the secret, updates `message`, `channel_post`, `my_chat_member`) and the bot's commands; it retries every minute until Telegram answers. The webhook takes no session or CSRF token: a request without the secret in `X-Telegram-Bot-Api-Secret-Token` gets 401. It acts on:

- `/start <code>` (also `/start@Bot <code>` in groups) or `/connect <code>` (channels): consumes the code and adds the chat as a verified destination of the code's organization (`hawkeye_bot = true`; `destination` is the chat ID, the label is the chat's name). The confirmation goes back to the chat as the webhook's response (`{"method":"sendMessage",…}`), so it costs no extra request. An expired or used code, a full list and an already-connected chat each get a plain explanation.
- `/stop`: removes this chat from every organization it alerts. The bot being removed or blocked (`my_chat_member` → `left`/`kicked`) does the same.
- A group upgraded to a supergroup (`migrate_to_chat_id`): the stored chat ID follows it.
- Anything else in a private chat gets a short help text; other group messages are ignored, and so are commands addressed to another bot.

Alerts to these chats are sent by the worker with the token from configuration, so rotating the token (`/revoke` in @BotFather) never touches stored destinations. A customer's own bot (`POST /notification-settings` with `bot_token`) is unaffected and always available.

### Heartbeat monitors

A heartbeat (`type: "heartbeat"`) monitors a cron job or scheduled task: HawkEye checks nothing, and the job requests its ping address each time it
runs. Available on every plan; a heartbeat counts toward the monitor limit like any monitor. `check_interval_seconds` is the **period**, the expected
time between pings (default 86400; from the plan's minimum interval up to 2592000, 30 days), and `grace_seconds` how late a ping may be before it
counts as missed (60–604800, default 3600). `url` is `""`, and the website-only fields (`url`, `regions`, `slow_threshold_ms`, `headers`, `body`,
`auth`, `assertions`, `expected_status_code`) must be left out (400 otherwise); `grace_seconds` on a website monitor is a 400 too. Heartbeats carry
`heartbeat: {ping_token, grace_seconds, last_ping_at, due_by}` (null for website monitors).

| Method & path | Who | Notes |
|---|---|---|
| `GET`, `HEAD` or `POST /ping/<token>` | **no login** (outside `/api`, no CSRF) | The job ran successfully. Plain-text `200 OK` |
| `… /ping/<token>/fail` | no login | The job failed |
| `… /ping/<token>/<0–255>` | no login | The job's exit status: 0 is success, anything else a failure. Other suffixes are 404 |

The first 1,000 characters of a `POST` body (for example the end of the job's output) are kept with the ping, with the time, method, source IP and
user agent; the last 100 pings per monitor are kept (`GET /monitors/:id/pings`). An unknown, malformed, rotated or deleted token is a plain-text 404.
Limits are in memory per API process: about one ping a second per address (burst 30) and a generous per-IP budget; over them, 429 with `Retry-After`.

**Timing.** A new heartbeat is `unknown` (waiting) with `due_by: null` until its first ping, and never alerts before then. Each ping sets
`last_ping_at` and `due_by` = ping + period (raised to the plan minimum, as for checks) + grace. When `due_by` passes, the sweeper
(`internal/heartbeats`, in the scheduler's worker) records a failed result (`error_category: "missed_ping"`, dated at the deadline) and the next
deadline a period later. A failure ping records one with `error_category: "job_failed"`. Heartbeats confirm on the first result (no streak): a miss
or a failure opens an incident at once (subject to the alert delay, quiet hours and maintenance windows, as for any monitor) and the next successful
ping resolves it. Uptime is therefore the share of expected runs that pinged successfully. A paused heartbeat logs pings (`ignored: true`) but changes
nothing; resuming it, or changing the period or grace time, restarts the deadline from now (resume) or from the last ping (edit).

**HawkEye's own downtime is never the customer's missed run.** Every API process marks `ping_intake` every 15 s. While no process has marked it for
90 s the sweeper decides nothing; when marking resumes after such a gap, the gap's end is recorded, and a heartbeat whose last ping is older than it
gets a full period plus grace after the gap before it can be called missed, because its ping may have been sent while HawkEye could not receive it.

Alerts for heartbeats say the job "did not check in on time" or "reported a failure" and "is checking in again"; webhook payloads carry
`monitor.type` (`http`, `heartbeat` or `dns`) and an empty `monitor.url` for heartbeats.

### DNS monitors

A DNS monitor (`type: "dns"`) looks up one record of a host name on a schedule. Available on every plan; it counts toward the monitor limit like any
monitor, and the plan's minimum interval and locations apply. `url` is the host name: it follows the free tools' public-host rules (no IP addresses, single
labels or private-network names), except that underscores are allowed so `_dmarc.example.com` works. A pasted URL is reduced to its host name. `dns_record_type`
is `A` (the default), `AAAA`, `MX`, `NS` or `TXT`. `dns_expected` (at most 255 characters, null or empty for any answer) is what an answer must match: an exact
address for `A` and `AAAA` (which must be public), an exact host name ignoring case and the final dot for `MX` and `NS`, and text contained in a record,
case-sensitive, for `TXT`. CNAME is not offered, because the resolver reports only where a chain of CNAMEs ends.

PATCH accepts the same fields. Changing `dns_record_type` without sending `dns_expected` clears the expected value, which was written for another kind of
record; either change resets the monitor's state, as a changed website target does. Both fields are a 400 on other monitor types. The website fields
(`slow_threshold_ms`, `headers`, `body`, `auth`, `assertions`, `expected_status_code`) must be left out (400 otherwise). DNS monitors carry
`dns: {record_type, expected}` (null for other monitors).

**Results.** A missing record or a failed lookup is `error_category: "dns"`, a wrong answer is `assertion`, and an answer that is not a public address is
`blocked`. `http_status_code` is null and `response_time_ms` is the lookup time. Summaries name the expected value and never the answers. Streaks, locations,
incidents, routing, alert delay, quiet hours, maintenance, status pages and badges work as for websites, and the host name's registered domain is tracked
for expiry. Alerts say "DNS check failing for" and "is resolving correctly again"; webhook payloads carry `monitor.type: "dns"` and the host name in
`monitor.url`.

### Multi-step API checks

A multi-step API check (`type: "workflow"`) runs a list of requests in order on each check and fails at the first step that fails
(`internal/checks/workflow.go`; plan in `docs/plan-synthetic-api.md`). Each counts as a monitor, and the plan also limits how many there are and how many
steps each may have (`plans.max_workflows`, `max_workflow_steps`: Free 1 of up to 3 steps, Pro 5 of 10, Agency 50 of 10; `PLAN_LIMIT_REACHED`
otherwise). The monitor's own website fields (`url`, `headers`, `body`, `auth`, `assertions`, `expected_status_code`) must be left out: the monitor's
`url` becomes the first step's address.

- `steps` (1 to 10): each has `name` (1 to 100 characters), `method`, `url`, `headers`, `body`, `auth`, `expected_status_code`, `assertions` and
  `timeout_seconds` (1 to 30, default 10), validated as for a website monitor, plus `extractions`: up to 10 values to save from the response for later steps,
  each `{name, from, path}` where `from` is `json_path` (a path such as `data.token`), `header` (a header name) or `cookie` (a cookie name), and `name`
  is a letter or `_` followed by up to 39 letters, digits or `_`.
- Later steps use a saved value as `{{name}}`, and a secret as `{{secret.name}}`, in their `url` path or query, header values, `body` and `auth` username
  and secret. Every value a step uses must be saved by an earlier step, and every secret must exist (400, on the field). Values can't appear in a
  step's scheme or host. In an address they are URL-encoded; in a body that starts with `{` or `[` they are JSON-escaped (write `"{{token}}"` in quotes);
  a value containing a line break is refused in a header or credentials.
- `secrets`: an object of name to value (1 to 2048 characters; up to 20). Values are write-only: responses list `secrets: [{name, set: true}]`. On PATCH a
  value replaces that secret, `null` removes it, and names left out are kept.
- `cookies` (default false): carry cookies from step to step during a run.
- A step's `auth.secret` is write-only too. On PATCH, `steps` replaces all the steps; a step sent without `auth.secret` keeps the secret stored for the
  step at the same position, if its `auth.type` is unchanged.
- A step using POST, PUT, PATCH or DELETE needs a verified address, and the check runs at most every 5 minutes (the default interval is raised to it).

Workflow monitors carry `workflow: {steps, secrets, cookies}` (null for other monitors). A run has 60 seconds in all. Its result is the failing step's,
with the step named: `"Step 2 (Get account): Received HTTP 401, expected 200"`; `response_time_ms` is the total of the steps, which slow-response alerts
use. A workflow that can't run as written (a value no step saved, or a path to save that isn't in the response) is `error_category: "workflow_config"`.
Each result's `steps` (in `GET /monitors/{id}/results`) lists the steps that ran: `{name, success, status_code, response_time_ms, error_category,
error_summary}`. Secrets and saved values are replaced with `[hidden]` in everything stored or alerted on; response content is never stored.

`POST /monitors/test-workflow` runs an unsaved workflow once and returns `{"result": {success, status_code, response_time_ms, error_category,
error_summary, steps}}`, storing nothing. The body is `{steps, secrets, cookies}`, validated as above against the plan's step limit; secrets must be sent
in full (stored ones are never read). It is session-only, needs a verified address for steps that write, and is limited per IP and to 60 runs an hour per
organization. A run longer than 25 seconds is stopped (`422 TEST_TOO_SLOW`). API keys can create, read, change and delete workflows like other monitors;
MCP's `get_monitor` lists their step names (`workflow_steps`), and `create_workflow` and `test_workflow` create and try them with secret names only
(see *MCP server*).

### Webhook and chat alerts

Every enabled, verified destination receives outage, recovery, certificate-expiry and domain-expiry alerts through the same durable queue as email (retries with backoff, at-least-once). A 2xx is success; 408, 429, 5xx and network errors are retried; any other 4xx (for example 404/410 for a revoked URL) fails the delivery permanently. Requests go through the SSRF-safe client, so private addresses are refused.

Webhook requests are `POST` with `Content-Type: application/json` and these headers: `X-HawkEye-Event` (`outage`, `recovery`, `ssl_expiry`, `domain_expiry`, `slow`, `slow_recovery`, `test`), `X-HawkEye-Delivery` (stable across retries; use it to drop duplicates), `X-HawkEye-Timestamp` (Unix seconds) and `X-HawkEye-Signature` = `sha256=` + hex HMAC-SHA256 of `"<timestamp>.<raw body>"` keyed with the signing secret. Reject requests whose signature does not match or whose timestamp is old. Body:

```json
{"event": "outage", "sent_at": "2026-10-12T15:00:00Z", "url": "https://app.example.com/incidents",
 "monitor": {"id": "…", "name": "Site", "type": "http", "url": "https://example.com"},
 "incident": {"started_at": "…", "resolved_at": null, "cause": "Received HTTP 503"}}
```

`ssl_expiry` carries `certificate: {expires_at, warning_days}` instead of `incident` (`warning_days` 30, 14, 7, 1, or 0 once expired); `domain_expiry` carries `domain: {name, expires_at, warning_days}` and no `monitor` (its `url` points at `/domains`); `slow` and `slow_recovery` carry `slowdown: {started_at, resolved_at, threshold_ms, peak_ms}` (`resolved_at` only on `slow_recovery`) instead of `incident`, and their `url` points at the monitor; `test` carries none of them. Chat destinations receive an equivalent, unsigned message in the app's own format: Slack incoming-webhook blocks; a Discord embed colored by severity, with mentions switched off (`allowed_mentions: {parse: []}`) so names such as `@everyone` never ping; a Teams Adaptive Card with an "Open in HawkEye" button; and a Telegram `sendMessage` in HTML mode with link previews off. Text from monitors (names, addresses, causes) is escaped for each app's markup so it cannot add links or formatting.

## Slow-response alerts

A monitor with `slow_threshold_ms` set is **slow** once its successful checks have taken longer than the threshold for `slow_checks` checks in a row, and
**back to normal** once they have taken at most the threshold for as many checks in a row; one slow (or one quick) response never changes anything. A
response exactly at the threshold is normal. Failed checks are left out: they are outages, reported as such, and neither extend nor end slowness. With
several check locations each location keeps its own streak and verdict, and the monitor is slow while a **majority** of its locations are, so one distant
location alone never raises an alert. Results recorded during a maintenance window are ignored, like everything else about health. Available on every plan; off by default.

Becoming slow opens a **slowdown** (at most one open per monitor; `slowdowns`: `started_at` = when it was confirmed, `threshold_ms`, `peak_ms` = the slowest
response while it lasted) and queues a `slow` alert to every enabled, verified recipient; returning to normal resolves it and queues `slow_recovery`. Both use
the delivery queue (retries, one per slowdown, recipient and event). A slowdown is **not an incident**: the site is online, so it does not count against
uptime, does not appear on the status page and does not open an incident. Monitors carry `slow_since` (the open slowdown's start, else null); the app shows
the monitor as "Slow".

Changing `slow_threshold_ms` or `slow_checks`, or anything that resets the monitor's health (its request or locations), starts the streaks again and closes
an open slowdown **without** a `slow_recovery` alert, because nothing got faster. Resolved slowdowns are deleted with the plan's history window, like incidents.

## Uptime badges

An embeddable SVG showing a monitor's uptime, **off by default** and turned on per monitor by the owner. Available on every plan. A badge is addressed by a random 22-character token, never by the monitor's id; turning it off clears the token (the address stops working at once, within a minute on other API processes), and turning it on again issues a new one.

| Method & path | Who | Notes |
|---|---|---|
| `POST /monitors/:id/badge` | owner | → 200 `{badge_token}`. Keeps an existing token, so embedded badges keep working |
| `DELETE /monitors/:id/badge` | owner | 204 |
| `GET /badge/<token>.svg` | **no login** (outside `/api`) | The large badge (400 × 84); `?style=small` gives the README size (200 × 28). `image/svg+xml`, `Cache-Control: public, max-age=300`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'`. 404 for an unknown, revoked or malformed token and for a deleted monitor. Served from a one-minute in-memory cache with a generous in-memory per-IP limit (requests come largely from image proxies such as GitHub's camo) |

Monitors carry `badge_token` (null while off). A badge shows only the uptime percentage (truncated to two decimals, as everywhere: never rounded up to 100%), the period and the state, never the monitor's name or address. The period is 30 days, or 7 on a plan that keeps less history (Free). The state is **up** or **down** from the monitor's current status, and **unknown** while it is paused or in a maintenance window (the percentage is still shown); with fewer than 5 checks in the period the badge says "Not enough data yet" instead of a number. The templates are `internal/badges/templates/*.svg` (the design's own SVG with only those values filled in, all escaped).

## SSL certificate expiry

Every monitor object carries `ssl`: `null` until a check has seen the certificate (and always for `http://` monitors), otherwise
`{expires_at, checked_at, days_remaining, status}` with `status` `valid` (more than 30 days left), `expiring` (30 or fewer) or `expired`;
`days_remaining` is whole days and negative once expired. It is read on **every check** from the TLS handshake with the monitored host itself (never from a host the
request is redirected to), and it is also recorded when the handshake failed *because the site's certificate had expired*. Changing a monitor's URL clears it. Available on every plan.

**Warning emails** go to the organization's enabled, verified alert recipients when the certificate first falls inside a bucket: **30, 14, 7 and 1 day(s)** before expiry, and once more
when it has **expired** (bucket 0). They use the same delivery queue as outage alerts (`event_type = 'ssl_expiry'`, retries, idempotency). Each (monitor, recipient, certificate expiry date, bucket) is sent at most once;
a renewed certificate (new expiry date) restarts the schedule. A recipient added after a bucket was handled does not receive a backlog. Paused and deleted monitors send nothing.

## Domain expiry

HawkEye watches when the *registration* of each domain expires, so a lapsed domain does not take a website and its email down unnoticed. A tracked domain is a **registrable domain** (`example.com`, `example.co.uk`), reduced from a host with the public suffix list. Names under suffixes that are not run by a registry (`*.vercel.app`, `*.github.io`) and IP addresses have no registration of the customer's to track and are ignored.

Domains come from two places. **Derived:** every live monitor's host brings its domain; the domain goes when no monitor uses it any more. **Added by hand:** an owner can add one (for a domain they own but do not monitor); it stays until removed. Both appear in one list.

| Method & path | Who | Notes |
|---|---|---|
| `GET /domains` | any member | `{domains: [Domain], limit}` by name. `limit` is the plan's `max_domains` |
| `POST /domains` | owner | `{name}` → 201 `{domain}`. `name` may be a host or a URL (`https://www.example.com/shop` tracks `example.com`). Adding a domain that is already tracked only pins it, needing no free place. 400 `VALIDATION_ERROR` (not a registrable domain), 403 `PLAN_LIMIT_REACHED` (`limit_type: "domains"`) |
| `DELETE /domains/:id` | owner | 204. Only domains added by hand can be removed; one that monitors also use stays in the list as derived. 409 `DOMAIN_IN_USE` for a purely derived domain |

Domain: `{id, name, manual, monitor_count, tracked, status, expires_at, days_remaining, source, unavailable_reason, checked_at, lookup_error, created_at}`.
`status` is `pending` (not looked up yet), `unavailable`, or for a known date `valid` (more than 30 days left), `expiring` (30 or fewer) or `expired`.
`tracked` is false for domains beyond the plan's limit (the oldest `max_domains` are tracked): they are listed but never looked up and raise no warnings; upgrading brings them in. Only manual adds are refused at the limit, so a monitor is never blocked by it.

**Where dates come from.** A daily job in the worker (`internal/domains`) asks the top-level domain's RDAP server (found through IANA's bootstrap file), and falls back to WHOIS on port 43 for registries without RDAP or whose RDAP record has no expiry date. Registries differ: some redact or never publish the date. Then `status` is `unavailable` and `unavailable_reason` says why (the UI shows "Expiry unavailable", and tells the customer that is not a statement the domain is safe). A lookup that merely *fails* (timeout, rate limit, registry outage) changes nothing the customer sees apart from `lookup_error`, and is retried after 1 h, 2 h, 4 h … up to a day. `source` is `rdap` or `whois`. Lookups use the SSRF-safe client; WHOIS server names (learned from IANA) are resolved and must be public addresses. Each name is looked up once per pass, however many organizations track it.

**Warning emails** use the schedule and the delivery queue of certificate warnings (`event_type = 'domain_expiry'`): to the enabled, verified recipients when the registration first falls inside a bucket of **30, 14, 7 and 1 day(s)**, and once more when it has **expired**. Each (domain, recipient, expiry date, bucket) is sent at most once; a renewal (new expiry date) restarts the schedule; a recipient added after a bucket was handled gets no backlog. Available on every plan, within the plan's domain limit.

## Notification delivery

One logical notification exists per (incident, recipient, event) — or, for certificate warnings, per (monitor, recipient, certificate expiry, bucket), for domain warnings per (domain, recipient, expiry, bucket), and for slow-response alerts per (slowdown, recipient, event) — a unique constraint — created in the same transaction as the incident,
so it cannot be lost or duplicated. The worker's dispatcher claims due deliveries with a visibility timeout and sends them; transient
failures retry with backoff 1 m, 5 m, 15 m, 1 h, 3 h up to `NOTIFY_MAX_ATTEMPTS` (default 6), then the delivery is marked `failed`.
Permanent errors (`email.ErrPermanent`) fail immediately. Delivery is at-least-once: a crash between sending and recording can cause one duplicate email.
Settings disabled or removed after queuing are skipped. A delivery held by an alert delay or quiet hours is queued with a later `next_attempt_at` and is
left alone until then (see *Alert routing, alert delay and quiet hours*).

## Contact form

| Method & path | Auth | Notes |
|---|---|---|
| `GET /contact/categories` | – | the triage topics: `{categories: [{code, label, hint, priority}]}` |
| `POST /contact` | – (session optional) | `{category, name, email, subject, message, url?}` → 202 `{reference}`. Emails the support inbox with Reply-To = `email`; a signed-in sender's account id and plan are added by the server. 400 `VALIDATION_ERROR` with `details.fields`; 429 when rate-limited; 503 `CONTACT_UNAVAILABLE` if delivery failed. A non-empty `company` field (spam trap) is answered 202 and silently dropped. |

## Feature waitlists ("Get notified")

Sign-ups for coming-soon features from the landing page's "Coming soon" section (`internal/waitlist`). Features: `status_page_domains` (status pages on your own domain), `status_page_subscribers` (email updates for status page visitors).

| Method & path | Auth | Notes |
|---|---|---|
| `POST /waitlist` | – (session optional) | `{feature, email?, company?}`. Signed in with a verified address and no other `email` → 200 `{status: "added" \| "already"}`, on the list at once. Otherwise → 202 `{status: "check_email"}` and a confirmation email (link valid 48 h); the answer is the same whether or not the address was already on the list, over its email budget, or the `company` spam trap was filled in, so it never reveals who signed up. 400 `VALIDATION_ERROR` for an unknown feature or bad address; 429 when rate-limited (per IP: the shared limit plus 20 an hour). |
| `POST /waitlist/confirm` | – | `{token}` from the email → 200 `{feature}` (its name). 400 `INVALID_TOKEN` if used, expired or unknown. |
| `POST /waitlist/leave` | – | `{token}` from any waitlist email → 204, removing that sign-up. Unknown tokens also get 204. |

The pages at `/waitlist/confirm` and `/waitlist/leave` ask before calling these, because mail scanners open links. The account export lists an address's sign-ups (`waitlists`), and deleting the account removes every sign-up with its address.

## Free tools

| Method & path | Auth | Notes |
|---|---|---|
| `POST /tools/ssl-check` | – | `{host}` (a domain name; a pasted URL is reduced to its host) → 200 with the certificate report: `{host, checked_at, status (valid, expiring, expired, invalid), days_remaining, trusted, hostname_match, tls_version, cipher_suite, problems: [{code, severity, message}], certificate: {subject_name, issuer_name, not_before, not_after, dns_names, key_algorithm, key_bits, signature_algorithm, sha256_fingerprint, serial_number, …}, chain: [...]}`. Needs the CSRF header like every POST. Errors: 400 `INVALID_HOST` (IP address, other port, private-network name, credentials, malformed); 422 `HOST_NOT_FOUND`, `HOST_NOT_ALLOWED` (resolves to a non-public address), `CONNECT_FAILED`, `NO_TLS`; 504 `HOST_TIMEOUT`; 429 `RATE_LIMITED` (per IP) or `HOST_RATE_LIMITED` (that host was checked 5 times in 10 minutes by anyone); 503 `BUSY` (8 checks already running) or `TOOL_UNAVAILABLE`. 429/503 carry `Retry-After`. Results are cached for 60 s per host. Only port 443 is ever contacted and nothing is sent after the handshake. |
| `POST /tools/site-check` | – | `{host}` (a domain; any path or query is ignored, the request is always `GET /`) → 200 `{host, url, checked_at, verdict (up, error_response, down), reason (ok, http_4xx, http_5xx, dns, connect, tls, timeout, redirect, other), status_code?, response_time_ms?, speed? (fast ≤ 800 ms, moderate, slow > 1800 ms), final_url?, https_error?}`. A site that does not answer is a 200 with `verdict: down`; errors are as for `/tools/ssl-check` (400 `INVALID_HOST`, 422 `HOST_NOT_ALLOWED`, 429 `RATE_LIMITED`/`HOST_RATE_LIMITED`, 503 `BUSY`/`TOOL_UNAVAILABLE`, 504 `HOST_TIMEOUT`). Results are cached for 30 s per host. |

## Plan limits

`403 PLAN_LIMIT_REACHED` with details:

```json
{"error": {"code": "PLAN_LIMIT_REACHED",
  "message": "Your Free plan includes up to 5 monitors and all of them are in use.",
  "details": {"limit_type": "monitors", "limit": 5, "used": 5, "plan": "free",
              "upgrade_url": "https://app.example.com/billing",
              "next_plan": {"code": "pro", "name": "Pro", "max_monitors": 50, "monthly_price_cents": 1900}}}}
```

`limit_type` is `monitors`, `check_interval` (limit = minimum seconds), `team_members`, `regions`, `domains`, `status_pages`, `status_page_branding`, `status_page_white_label`, `maintenance_windows`, `reports` or `branded_reports`. If an organization is *above* its limit
after a downgrade, existing monitors (and status pages) are untouched, creation is refused, the message says how many to delete, and `usage.over_limit` is true.

## Known trade-offs

- `EMAIL_TAKEN` reveals that an address is registered (better UX than a silent no-op). Registration is rate-limited; revisit if enumeration matters.
- Per-IP rate limits live in PostgreSQL and hold across API replicas (`internal/ratelimit`). The free checkers' per-host limit, concurrency cap and result cache (`internal/toolguard`) are per API process.
