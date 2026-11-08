# Connectors Directory submission (Phase 3c)

How to list HawkEye's MCP server in Anthropic's Connectors Directory: what to enter in each step of the developer portal, how to prepare production
and the reviewer account, and what is still open. Requirements were checked against Anthropic's docs on 2026-11-08
(claude.com/docs/connectors/building/submission, …/review-criteria, …/authentication, claude.com/docs/directory/publish); read them again
before submitting, since they change.

## Where things stand

| Requirement | Status |
|---|---|
| Remote MCP server over HTTPS | Built (`/mcp`, PR #11); live once `main` is deployed |
| Every tool has a title and `readOnlyHint` or `destructiveHint` | Done, pinned by a test; checked with MCP Inspector `--strict` |
| Separate read and write tools; no catch-all request tool | Done |
| OAuth 2.0 for user accounts, S256 PKCE, discovery metadata, `401` with `resource_metadata` | Done (PR #13) |
| Claude's redirect URI `https://claude.ai/api/mcp/auth_callback` | Registered per environment with `cmd/oauthclient` (step 3 below) |
| Consent screen shows where the person goes back to | Done (this branch) |
| Rotating refresh tokens, `invalid_grant` for dead ones, form-encoded token endpoint | Done |
| Origin validation | Done (`DefaultMCPOrigins`) |
| Actionable errors, reasonable response sizes, no conversation data | Done |
| First-party API on our own domain | Yes |
| Public documentation | `https://hawkeye.observer/docs/claude` (update after listing, see the end) |
| Privacy policy | Written; **needs the lawyer's review** (`docs/legal-placeholders.md`) |
| Test account with realistic data | `scripts/seed_demo.py` (step 4 below) |
| Tested as a custom connector in claude.ai | **To do**, after deploying (step 5 below) |

Not needed for the listing: dynamic client registration and Client ID Metadata Documents (we use Anthropic-held client credentials, which Anthropic
recommends for directory traffic). Claude Code doesn't use the directory client; it connects with an API key (`/docs/claude`). Supporting Client ID
Metadata Documents later would let Claude Code and custom connectors connect by signing in without a client being registered first.

## Runbook

1. **Legal review.** Have the Privacy Policy and Terms reviewed (they now cover API keys, two-factor sign-in and connected apps). Submit only
   after that: the portal needs the privacy policy URL.
2. **Deploy `main` to production** as usual (`docs/deployment.md`). Migrations `000033`–`000035` apply. `SECRETS_ENCRYPTION_KEY` must be set
   (two-factor secrets are sealed with it). Check `https://hawkeye.observer/.well-known/oauth-authorization-server` and
   `https://hawkeye.observer/.well-known/oauth-protected-resource/mcp` answer, and that `POST /mcp` without a token answers `401` with a
   `WWW-Authenticate: Bearer … resource_metadata=…` header.
3. **Register Claude** on the production server, in the deploy folder, once a release with `oauthclient` in the API image is deployed
   (`docs/operations.md#oauth-clients-connecting-claude`):

   ```sh
   docker compose --env-file .env.production -f deploy/docker-compose.prod.yml run --rm --entrypoint /usr/local/bin/oauthclient migrate \
     create -name Claude -redirect https://claude.ai/api/mcp/auth_callback
   ```

   Keep the client ID and secret in the password manager until step 7; the secret is shown once.
4. **Make the reviewer account.** Sign up at hawkeye.observer with an address you control (for example `directory-review@…`), verify it, put
   the organization on **Agency** (a complimentary subscription, so write tools and maintenance work), and leave two-factor sign-in **off** for
   it. As its owner, create a **read and write** API key, then fill it:

   ```sh
   HAWKEYE_API_KEY=hk_… scripts/seed_demo.py --base-url https://hawkeye.observer --status-page-slug hawkeye-demo
   ```

   It adds two clients' worth of monitors (websites, DNS, heartbeats, one paused), a published status page, a weekly maintenance window, and a
   resolved incident with three updates. It can be run again safely. Revoke the API key afterward; the reviewer signs in through OAuth.
5. **Test as a custom connector in claude.ai** (the portal asks you to confirm this): in Claude, add a custom connector with URL
   `https://hawkeye.observer/mcp`, open its advanced settings, enter the client ID from step 3 (and the secret), connect, and approve on HawkEye's
   consent page with the reviewer account. Then ask Claude to call each tool at least once, for example:
   - "What's the state of my HawkEye monitors?" (`get_account_overview`, `list_monitors`)
   - "Show me the Acme Bakery website monitor" (`get_monitor`), "What was uptime this week?" (`get_uptime`)
   - "List incidents" and "show the invoice export incident" (`list_incidents`, `get_incident`), "List my status pages and maintenance windows"
   - "Pause and resume the Acme Bakery monitors" (`pause_monitors`, `resume_monitors`), "Add a website monitor for https://example.com"
     (`create_monitor`), "Schedule maintenance tonight at 2am for 30 minutes" (`schedule_maintenance`)
   - "Post an update on that incident" (`post_incident_update`, Claude asks first), then delete it and the maintenance window
     (`delete_incident_update`, `delete_maintenance_window`, Claude asks first)

   Delete the test monitor and disconnect the app afterward, so the account is as `seed_demo.py` left it.
6. **Prepare the listing assets:** the icon `web/src/assets/brand/app-icon.png` (256×256 PNG, the gold-ringed badge), and the copy below.
7. **Submit** at claude.ai/directory/manage → **Submit new** → **MCP connector**, from the HawkEye company's paid Claude account, and enter the
   answers below. Afterward, watch the portal for reviewer feedback; escalations go to `mcp-review@anthropic.com`.

## Portal answers

### 1. Connection

- URL: `https://hawkeye.observer/mcp`
- Users connect to: **Universal URL** (everyone uses the same URL)

### 2. Tools

Synced from the server. Expect 15 tools, 8 read-only and 7 writes, with no warnings about missing titles or annotations.

### 3. Listing

- **Server name** (≤ 100): `HawkEye`
- **One-liner** (≤ 200):

  > Check your websites, APIs, cron jobs and DNS monitoring from Claude: see what's down, review uptime and incidents, pause monitors and post status page updates.

- **Description** (≤ 2,000):

  > HawkEye monitors websites, APIs, scheduled jobs and DNS records for freelancers, agencies and small teams, and alerts them when something goes wrong. This connector brings your HawkEye account into Claude, so you can ask about your monitoring in plain language and act on it without switching tabs.
  >
  > Ask Claude what's down right now, how a client's site performed this month, or what happened during last night's outage, and it answers from your live monitors, uptime history and incidents. It can also list your public status pages and maintenance windows.
  >
  > With read and write access on a Pro or Agency plan, Claude can pause and resume monitors (one at a time or every monitor with a client's tag, for example during a deploy), add website, cron job and DNS monitors, and schedule maintenance. Organization owners can also have Claude post updates on an incident, which appear on their public status pages. Claude always asks before anything public or destructive.
  >
  > You choose the organization and the access level when you connect, and you can disconnect at any time from your HawkEye account. The connector acts as you, with your permissions, and never sees passwords, billing, team settings or monitor credentials.
  >
  > Requires a HawkEye account (the free plan works for reading). Changes need Pro or Agency.

- **Categories** (1–5): choose the closest of the portal's options, such as Developer tools, Monitoring or observability, and Productivity.
- **Documentation URL:** `https://hawkeye.observer/docs/claude`
- **Privacy policy URL:** `https://hawkeye.observer/privacy`
- **Support contact:** the support address that receives contact-form mail (`CONTACT_TO_ADDRESS` in production)
- **Icon:** `web/src/assets/brand/app-icon.png`
- **URL slug:** `hawkeye` (permanent once published)

### 4. Use cases

- **Primary use cases:** checking which monitors are down and why; reviewing uptime and response times for a site or client; reading incident
  history; pausing monitors during deploys or planned work; adding new monitors; posting status page updates during an incident; scheduling
  maintenance windows.
- **What users need first:** a HawkEye account (sign up free at hawkeye.observer). Reading works on every plan; changes need Pro or Agency, and
  posting incident updates and scheduling maintenance need the organization's owner.
- **Reads or writes:** both.

### 5. Company

HawkEye, `https://hawkeye.observer`, and the primary contact for review updates.

### 6. Authentication

- Authentication requirement: **Always required**
- Authentication method: **OAuth sign-in** → OAuth client: **Anthropic-held client credentials** → client ID from step 3 → **Anthropic holds a
  client secret** → the secret from step 3

### 7. Data handling

- The API is **our own** (first party), on our domain.
- No personal health data. No sponsored content.

### 8. Test & launch

Reviewer instructions (fill in the account):

> Connect HawkEye from Claude with the URL https://hawkeye.observer/mcp. When HawkEye's sign-in page opens, sign in with the test account below, choose the organization "<organization name>" and "Read and make changes", and select Allow. The account is on the Agency plan and already has monitors for two example clients, a published status page (https://hawkeye.observer/status/hawkeye-demo), a weekly maintenance window, and a resolved incident with updates on the "Hourly invoice export" monitor.
>
> Email: <reviewer address>
> Password: <password>
>
> Every tool can be tried on this data. Posting an incident update makes it visible on the demo status page; delete it afterward with delete_incident_update. Please don't change the account's password or turn on two-factor sign-in.

Confirm that every tool was run (step 5).

### 9. Compliance

Seven acknowledgments: the directory guidelines, first-party API use (ours), financial transactions (none: no tool moves money), AI media
generation (none), prompt injection (tool descriptions only describe the tool; untrusted text is returned as data, `docs/security.md#mcp-server`),
conversation data collection (none stored), and public documentation (`/docs/claude`).

## After the listing is live

- Update `/docs/claude` (`web/src/pages/docs/DocsPages.tsx`): replace "will be available once HawkEye is listed in Claude's connector directory"
  with how to add HawkEye from Claude's connector directory, and add a "Works with Claude" line to the landing page and `llms.txt` (feature
  sync checklist). Don't advertise it before it is listed.
- Mark Phase 3c done in `docs/plan-api-mcp.md`.
- The portal's connector dashboard shows server health and usage by tool; `hawkeye_mcp_tool_calls_total` shows the same from our side.
