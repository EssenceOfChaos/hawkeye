# Plan: public API, MCP server and Claude connector

Status: **Phase 1 built** (2026-11-07, PR #10); **Phase 2 built** (2026-11-08, branch `feat/mcp-server`); Phase 3 proposed.

This plan pulls roadmap item #14 (Public API and API keys) forward from Q4 and builds two more things on top of it: an MCP server
that lets Claude and other AI clients use HawkEye, and a listing in Anthropic's Connectors Directory. It also brings forward roadmap
item #22 (two-factor sign-in), because OAuth lets outside apps act on a customer's account.

It runs as three phases. **Each phase ships and is useful on its own**, so the work can stop after any of them if priorities change
(custom domains, roadmap #20, is the item most likely to need the time).

| Phase | Delivers | Estimate | Ships as |
|---|---|---|---|
| 1. Token auth and API keys | A documented public API for scripts, dashboards and Terraform-style setups | 3–4 weeks | Roadmap #14 |
| 2. MCP server | HawkEye tools in Claude Code and any MCP client that can send a header | 1.5–2 weeks | "Use HawkEye with Claude" (beta) |
| 3. Two-factor sign-in, OAuth, directory submission | One-click connection from claude.ai, and a directory listing | 3.5–5 weeks | Roadmap #22 and the Claude connector |

Total: about 8.5–11 build weeks for one developer, including tests, docs and the feature sync sweep. The estimates are judgment calls in
the roadmap's style, not measurements.

## Decisions already made

1. **Plan gating.** Read access on every plan; write access (anything that changes data) on Pro and Agency. A Free organization can
   still create keys, but only read-only ones.
2. **Key and token scope.** A key belongs to **one organization and one person** (the member who created it). It has one of two
   scopes, `read` or `write`. A key can never do more than its owner can: an owner-only endpoint still needs the owner role, and the
   role is checked again on every request, so removing someone from the team disables their keys immediately.
3. **OAuth clients.** Start with **clients we register ourselves** (Anthropic holds Claude's client ID and secret, entered in the
   developer portal). Leave dynamic client registration for later, when Zapier or another client needs it; the schema supports both.
4. **Legal review.** Booked now, so it is finished before Phase 3 ships (see *Legal and privacy*).
5. **Key expiry.** Keys expire after 90 days by default, with warning emails before they do (see *Key management*).
6. **Organization-wide two-factor sign-in.** Owners can require it for every member (see *3a*).
7. **Publishing asks first.** `post_incident_update` is marked destructive, so Claude always asks before it runs (see *Tools*).

## Phase 1: token auth and API keys

### What exists today

- Sign-in is a session cookie only. `requireAuth` in [internal/httpapi/session.go](../internal/httpapi/session.go) reads
  `hawkeye_session`, and `resolveOrg` in [internal/httpapi/handlers_monitors.go](../internal/httpapi/handlers_monitors.go) picks the
  organization from `X-Organization-ID` and puts the caller's role on the context.
- Every `/api/v1` request goes through double-submit CSRF ([internal/httpapi/server.go](../internal/httpapi/server.go), `registerAPI`).
- Tokens are already random 256-bit values stored as SHA-256 hashes (`newToken` / `HashToken` in
  [internal/auth/tokens.go](../internal/auth/tokens.go)), and a shared PostgreSQL rate limiter exists
  ([internal/ratelimit](../internal/ratelimit/ratelimit.go)). Both are reused as they are.

### Data model (migration `000033_api_tokens`)

```sql
CREATE TABLE api_tokens (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind            text NOT NULL CHECK (kind IN ('key', 'oauth_access')),   -- Phase 3 adds oauth_access rows
  name            text NOT NULL,                     -- "Grafana dashboard"; for OAuth, the client's name
  scope           text NOT NULL CHECK (scope IN ('read', 'write')),
  prefix          text NOT NULL,                     -- first characters, shown in the UI so people can tell keys apart
  token_hash      bytea NOT NULL UNIQUE,
  oauth_grant_id  uuid,                              -- Phase 3
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_used_at    timestamptz,
  expires_at      timestamptz,                       -- null: does not expire (keys); always set for OAuth
  revoked_at      timestamptz
);
CREATE INDEX api_tokens_org ON api_tokens (organization_id) WHERE revoked_at IS NULL;
```

Plus one plan column, `api_write_enabled boolean` (false on Free, true on Pro and Agency), read through `internal/plans` like every
other limit. A per-organization cap on live keys (25) stops key sprawl.

**Key format:** `hk_` followed by the existing 43-character token. The fixed prefix lets GitHub secret scanning and our own logs
recognize a leaked key. The full key is shown once, at creation, and never again.

### Request flow

1. A new `authenticate` middleware replaces `requireAuth` on the routes tokens may use. With `Authorization: Bearer hk_…` it looks
   up the hash; otherwise it falls back to the session cookie exactly as today. It puts a *principal* on the context: the user, plus
   for tokens the organization, scope and token ID.
2. `resolveOrg` uses the token's organization and ignores `X-Organization-ID` (a header naming a different organization gets
   `403 TOKEN_ORGANIZATION_MISMATCH`, so a script bug fails loudly). It still loads the membership, so the role is current.
3. CSRF is skipped **only** when the request authenticates with a bearer token. Browsers never attach that header on their own, so
   CSRF protection isn't needed for it. A request carrying both a bearer token and a session cookie uses the token and ignores the cookie.
4. `requireWrite`: a `read` token gets `403 TOKEN_SCOPE` on any POST, PATCH, PUT or DELETE, and a `write` token on a Free
   organization gets `403 PLAN_LIMIT_REACHED`, using the existing error code.
5. Rate limits per token through `ratelimit.Limiter` (scope `token:<id>`): 120 requests a minute, 30 writes a minute, returned with
   `Retry-After`. The per-IP global limit still applies.
6. `last_used_at` is written at most once a minute per token, so tokens don't add a database write to every request.

### What tokens can and can't reach

Tokens work on the tenant-scoped group (`scoped` in `registerAPI`): monitors, tags, bulk actions, results, uptime, incidents and
their updates, status pages, maintenance windows, domains, regions, dashboard and usage. Owner-only routes there keep `requireOwner`.

Tokens are **refused** (`403 SESSION_REQUIRED`) on everything that would let a stolen key take over or harm the account:
`/auth/*`, `/account/*` (export, deletion, password), billing changes, team and invitation routes, ownership transfer, alert
recipient changes (they email third parties), and the key management routes themselves. This is done by registering those
groups with the cookie-only middleware, not with a deny list, so new routes are cookie-only unless someone opts them in.

### Key management

- `GET /api-keys`, `POST /api-keys` `{name, scope}` → `{key, token}` (token shown once), `DELETE /api-keys/:id`. Session only,
  verified email required. Members see and manage their own keys; the owner also sees and can revoke every key in the organization.
- **Expiry.** A key expires after 30, 90 (the default) or 365 days, on a custom date, or never. "Never" is offered for setups
  like Terraform but is never preselected. The worker emails the key's owner 7 days before a key expires and again when it does,
  reusing the weekly summary's once-only email pattern. An expired key answers `401 TOKEN_EXPIRED`, not the generic
  `UNAUTHENTICATED`, so a broken script says why it broke. Expiry can't be extended; make a new key instead.
- Creating a key emails its owner ("A new API key, *name*, was created"), in the style of the change-password notice.
- A password **reset** and deleting the account revoke that person's keys; leaving or being removed from the organization deletes them (a
  foreign key to the membership). Changing the password while signed in leaves keys working, the way most services treat personal tokens:
  the person can see and revoke them on the API keys page.
- UI: an **API keys** section in Settings (a new page next to `TeamPage.tsx`), with name, scope, prefix, created and last used.

### Kill switch

`DISABLE_API_KEYS=true` makes bearer authentication answer `503` without touching sessions, the same pattern as `DISABLE_FREE_TOOLS`.

### Tests

Integration tests in `internal/httpapi` (an `api_tokens_integration_test.go`) for: each scope on each method; Free versus paid
writes; owner-only routes with a member's key; refused routes; revoked, expired and removed-member keys; the organization mismatch;
CSRF skipped only for bearer requests; rate limits. One table test walks every registered route and asserts it is either
token-enabled on purpose or cookie-only, so a new route can't silently become reachable by tokens.

## Phase 2: MCP server

### Shape

- A Streamable HTTP MCP endpoint at `https://hawkeye.observer/mcp`, served by the API process (`cmd/api`), stateless (no MCP
  sessions to store), with a route in `deploy/Caddyfile`.
- Built with the official Go SDK, `github.com/modelcontextprotocol/go-sdk` (v1.8.0).
- Authentication is the same bearer token from Phase 1 (API keys now, OAuth access tokens in Phase 3). A missing or bad token gets
  `401` with a `WWW-Authenticate` header; Phase 3 adds the `resource_metadata` pointer that OAuth clients follow.
- **Origin check:** a request with an `Origin` header is rejected unless the origin is on a short allowlist (claude.ai, claude.com;
  confirm the current list against Anthropic's docs). Clients that are not browsers send no `Origin` and are allowed.
- A new package, `internal/mcpserver`, holds the tool definitions. Tools call the same service methods the HTTP handlers call
  (`monitors`, `incidents`, `reports`, …), never the HTTP API over loopback.

**Main risk: keeping the checks the same.** Some checks live in handlers rather than services (role checks through `requireOwner`,
some plan checks inline, for example branding). Each tool therefore declares its required scope and role, and a test asserts that
every tool's guards match its HTTP route's guards. Where a plan check lives only in a handler, move it into `internal/plans` or the
service first; that is the rule `internal/plans` already states.

### Tools (first version)

Each tool has a `title` and either `readOnlyHint: true` or an explicit `destructiveHint`, as the directory requires. Names are
short `snake_case`, with no catch-all request tool.

The MCP spec treats a write tool as destructive unless it says otherwise, so every write tool sets the hint: `destructiveHint: true`
for `post_incident_update` (it publishes text on public status pages) and the two deletes, so Claude always asks first and shows the
arguments; `false` for pause, resume, create and schedule. The hint is a request to the client, and other MCP clients may ignore
it, so `post_incident_update` also says in its description that the update is public, and returns the status pages it now appears on.

| Tool | Kind | Needs | Notes |
|---|---|---|---|
| `get_account_overview` | read | – | Plan, usage against limits, counts of monitors up, down and paused, open incidents |
| `list_monitors` | read | – | Filter by state, type or tag; paginated, 50 per page |
| `get_monitor` | read | – | Settings (never headers, body or credentials), current state, last results, SSL and domain expiry |
| `get_uptime` | read | – | Uptime % and response-time summary for a range, clamped to the plan's history |
| `list_incidents` | read | – | Open or recent incidents, filtered by monitor |
| `get_incident` | read | – | One incident with its timeline and posted updates |
| `list_status_pages` | read | – | Pages, slugs and which monitors they show |
| `list_maintenance_windows` | read | – | Current and upcoming windows |
| `pause_monitors` / `resume_monitors` | write | paid | Monitors by ID, or every monitor with a tag (uses the bulk path) |
| `create_monitor` | write | paid | Website, heartbeat or DNS; same validation and plan limits as the API |
| `post_incident_update` | write | paid, owner | Shown on public status pages, so the description says so plainly |
| `schedule_maintenance` | write | paid, owner | One-off windows only in the first version |
| `delete_incident_update`, `delete_maintenance_window` | write, destructive | paid, owner | Claude always asks before running these |

Left out of the first version: deleting monitors, editing alert recipients, billing, team changes and status page edits. These can
be added later without resubmitting.

### Response rules

- Small, summarized responses with IDs Claude can pass to the next tool, and pagination instead of dumps.
- Errors explain the fix: "Your Free plan can't make changes through the API. Upgrade to Pro, or use a read-only tool." The
  existing `APIError` codes map to these messages in one place.
- **Untrusted text.** Monitor names, incident updates and check error text can contain anything (error text can include parts of a
  customer's site). Tools return them as labeled data fields, never in a way that reads as instructions, and never return response
  bodies. Tool descriptions say what the tool does and never tell Claude how to behave, which is the directory's prompt-injection rule.
- No conversation data is stored. Logs record tool name, organization, token ID, duration and outcome only.

### Metrics and docs

- Prometheus: `mcp_tool_calls_total{tool, outcome}` and a latency histogram, beside the existing API metrics.
- A public docs page, "Use HawkEye with Claude": creating a key, and
  `claude mcp add --transport http hawkeye https://hawkeye.observer/mcp --header "Authorization: Bearer hk_…"`.

### Testing

Unit tests for every tool (valid input, invalid input, wrong scope, wrong plan, wrong role), then each tool by hand in MCP Inspector
and in Claude Code. Testing from claude.ai as a custom connector probably needs Phase 3's OAuth.

## Phase 3: two-factor sign-in, OAuth and the directory

### 3a. Two-factor sign-in (TOTP), roadmap #22

- Authenticator-app codes plus ten single-use recovery codes, optional per user. Migration for `user_totp` (secret sealed with
  `SECRETS_ENCRYPTION_KEY`, as monitor credentials are) and `recovery_codes` (hashed).
- Login becomes two steps when two-factor sign-in is on: password, then code. Rate-limit code attempts per user and per IP.
- Turning it on or off, or using a recovery code, emails the user. Turning it off needs the current password and a code.
- The OAuth consent step (below) asks for a fresh code when two-factor sign-in is on, so a stolen session cookie alone can't grant
  an app access.
- **Required for an organization** (`organizations.require_two_factor`, **off by default**). It's the owner's choice: nobody is made
  to use two-factor sign-in unless their organization's owner turns this on, and the owner can turn it off again at any time. Only
  an owner who has two-factor sign-in on can turn it on.
  Members without it get a 7-day grace period, with an in-app banner and an email. After that they can't open that organization
  until they set it up, and their API keys and OAuth grants for it stop working, so tokens can't be used to get around the rule.
  Their own account and other organizations are unaffected. This mostly matters for Agency (the only plan with several seats
  today). It adds about 3–5 days, and the directory doesn't depend on it, so it's the first part of Phase 3 to cut if time runs short.

### 3b. OAuth 2.1 authorization server

Only the parts MCP clients need, built on the existing hashed-token code rather than a full OAuth framework. That keeps
it to a few hundred lines we can review closely; a library like `ory/fosite` would bring its own storage layer, duplicating ours.
Reassess this if the scope grows (for example dynamic client registration).

| Endpoint | Purpose |
|---|---|
| `GET /.well-known/oauth-protected-resource` | RFC 9728 metadata for `/mcp`, naming our authorization server |
| `GET /.well-known/oauth-authorization-server` | RFC 8414 metadata: endpoints, `S256` PKCE only, scopes `read` and `write` |
| `GET /oauth/authorize` | Shows the consent page (a React page): app name, organization picker, scope choice |
| `POST /api/v1/oauth/consent` | Session plus CSRF; records the grant and returns a one-time code to the redirect URI |
| `POST /oauth/token` | `authorization_code` (PKCE required) and `refresh_token` grants |
| `POST /oauth/revoke` | RFC 7009 revocation |

Rules:

- **Clients** live in `oauth_clients` (ID, hashed secret, name, exact redirect URIs), added with a `cmd/migrate`-style admin command.
  Claude's redirect is `https://claude.ai/api/mcp/auth_callback`; confirm the full current list against Anthropic's docs.
- Redirect URIs must match exactly. Codes are single-use, expire after 60 seconds, and are bound to the client, redirect URI and PKCE
  challenge. `state` is passed through untouched.
- **Access tokens** are opaque `hk_` tokens, stored in `api_tokens` with `kind = 'oauth_access'`, expire after an hour, and are
  limited to `/mcp` (the resource they were issued for). Phase 1's middleware therefore handles them with no new code path.
- **Refresh tokens** last 30 days and rotate on every use. If a refresh token is used twice, the whole grant is revoked (reuse
  means it leaked).
- A Free organization can only grant `read`; the consent page doesn't offer `write`.
- **Connected apps** appear in the API keys page with a revoke button, and connecting a new app emails the user.
- The consent page is already protected from framing by the `frame-ancestors 'none'` and `X-Frame-Options: DENY` headers.

### 3c. Directory submission

Prerequisites, then the portal at claude.ai/directory/manage:

- [ ] Legal review finished and the Privacy Policy updated (below)
- [ ] Every tool tested in MCP Inspector and as a custom connector in claude.ai
- [ ] Public docs page live (Phase 2's page plus OAuth connection steps)
- [ ] A demo organization with realistic data (a seeding script in `scripts/`: monitors of each type, a past incident with updates,
      a status page, a maintenance window) and reviewer credentials for it, with two-factor sign-in off
- [ ] Listing copy: name, one-liner (200 characters), description (2,000), categories, icon from `docs/branding.md`, support contact,
      allowed link URIs (`https://hawkeye.observer`)
- [ ] Data handling answers: first-party API, no health data, no sponsored content
- [ ] The seven compliance acknowledgments

After submitting: an automatic scan, then a Community listing by default. Watch the portal for reviewer feedback; escalations go to
`mcp-review@anthropic.com`.

## Security review items

Add a section to [docs/security.md](security.md) and the residual risks to [docs/security-review.md](security-review.md):

- Leaked keys: hashed at rest, recognizable prefix, shown once, revocable, `last_used_at` visible, creation emailed.
- Tokens can't reach account takeover routes (enforced by route grouping and the route table test).
- Role and plan are checked again on every request; a removed member's keys stop working immediately.
- CSRF is skipped only for bearer authentication.
- OAuth: PKCE required, exact redirect match, single-use codes, rotating refresh tokens with reuse detection, consent behind a fresh
  two-factor code when two-factor sign-in is on.
- MCP: Origin allowlist, no secrets or response bodies in tool output, untrusted text labeled as data.
- Abuse: per-token rate limits on top of the per-IP limit; a kill switch.

## Legal and privacy

For counsel, added to [docs/legal-placeholders.md](legal-placeholders.md):

- New data: API key metadata (name, prefix, last used), OAuth grants (client, scope, time), TOTP secrets and recovery codes.
- Customers can let third-party apps, including AI assistants such as Claude, read and change their HawkEye data. What goes to
  those apps is under the customer's control and the app's own terms. The Privacy Policy should say so.
- We store no conversation content from MCP clients.

## Feature sync

Each phase sweeps the usual list (pricing cards in `PlanDetails.tsx`, `planCopy.ts`, landing page value strip and FAQ, public docs
pages and `seo.ts`, the Caddyfile docs allowlist, `llms.txt`, `README.md`, `docs/api.md`, `docs/architecture.md`, `docs/operations.md`,
emails, sync tests). Advertise only what has shipped: "API access" on the pricing cards after Phase 1, "Works with Claude" after Phase 3.

## How we will know it worked

- Organizations with at least one active key, and requests per key (the roadmap's existing Q4 measures).
- MCP tool calls per week and the share of them that are writes.
- Connected claude.ai accounts, and signups that mention Claude in the contact form or onboarding.
- Free-to-paid upgrades that follow a `PLAN_LIMIT_REACHED` from a write attempt through the API or MCP.

