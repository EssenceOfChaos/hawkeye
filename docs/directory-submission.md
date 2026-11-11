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
| Privacy policy | Approved by counsel, 2026-11-08 (`docs/legal-placeholders.md`) |
| Test account with realistic data | `directory-reviewer@hawkeye.observer`, Agency (100%-off coupon), filled with `scripts/seed_demo.py`; demo heartbeats pinged by cron (below) |
| Tested as a custom connector in claude.ai | Confirmed in the portal |
| **Submitted** | **2026-11-08.** Watch the portal for status and reviewer feedback |

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
   cd ~/hawkeye
   docker compose --env-file .env.production -f deploy/docker-compose.prod.yml -f deploy/docker-compose.local-db.yml \
     run --rm --entrypoint /usr/local/bin/oauthclient migrate create -name Claude -redirect https://claude.ai/api/mcp/auth_callback
   ```

   Use the same Compose files as `scripts/deploy.sh` (production runs its database from `docker-compose.local-db.yml`; leave that file out with
   a managed database). Keep the client ID and secret in the password manager; the secret is shown once.
4. **Make the reviewer account.** Sign up at hawkeye.observer with an address you control (for example `directory-review@…`), verify it, put
   the organization on **Agency** (a complimentary subscription, so write tools and maintenance work), and leave two-factor sign-in **off** for
   it. As its owner, create a **read and write** API key, then fill it:

   ```sh
   HAWKEYE_API_KEY=hk_… scripts/seed_demo.py --base-url https://hawkeye.observer --status-page-slug hawkeye-demo
   ```

   It adds two clients' worth of monitors (websites, DNS, heartbeats, one paused), a published status page, a weekly maintenance window, and a
   resolved incident with three updates. It can be run again safely. Revoke the API key afterward; the reviewer signs in through OAuth.

   **Keep the demo heartbeats alive.** Nothing pings the two heartbeat monitors after seeding, so "Hourly invoice export" would go down about 70
   minutes later (opening an incident, alerting the reviewer address and showing on the demo status page). The production server's `deploy`
   crontab pings both, in two lines tagged `hawkeye-demo-heartbeat` (installed 2026-11-08):

   ```text
   5 * * * *  curl -fsS -m 10 -o /dev/null https://hawkeye.observer/ping/<hourly invoice export token>
   30 2 * * * curl -fsS -m 10 -o /dev/null https://hawkeye.observer/ping/<nightly database backup token>
   ```

   The tokens are on each monitor's page. If the account is rebuilt (or the server is: `scripts/rebuild.sh`), add the lines again.
5. **Test as a custom connector in claude.ai** (the portal asks you to confirm this): in Claude, add a custom connector named `HawkEye` with URL
   `https://hawkeye.observer/mcp`, open its advanced settings, enter the client ID from step 3 and the secret (required: HawkEye has no dynamic
   client registration, so without them connecting fails), connect, and approve on HawkEye's consent page with the reviewer account. Then, in a
   claude.ai chat with the HawkEye connector turned on, ask Claude to call each tool at least once, for example:
   - "What's the state of my HawkEye monitors?" (`get_account_overview`, `list_monitors`)
   - "Show me the Acme Bakery website monitor" (`get_monitor`), "What was uptime this week?" (`get_uptime`)
   - "List incidents" and "show the invoice export incident" (`list_incidents`, `get_incident`), "List my status pages and maintenance windows"
   - "Pause and resume the Acme Bakery monitors" (`pause_monitors`, `resume_monitors`), "Add a website monitor for https://example.com"
     (`create_monitor`), "Schedule maintenance tonight at 2am for 30 minutes" (`schedule_maintenance`)
   - "Post an update on that incident" (`post_incident_update`, Claude asks first), then delete it and the maintenance window
     (`delete_incident_update`, `delete_maintenance_window`, Claude asks first)

   Delete the test monitor and disconnect the app afterward, so the account is as `seed_demo.py` left it.
6. **Prepare the listing assets:** the icon (the portal asks for a URL: `https://hawkeye.observer/icon-192.png`, the gold-ringed badge, 192×192
   PNG), and the copy below.
7. **Submit** at claude.ai/directory/manage → **Submit new** → **MCP connector**, from the HawkEye company's paid Claude account, and enter the
   answers below. Afterward, watch the portal for reviewer feedback; escalations go to `mcp-review@anthropic.com`.

## Portal answers

### 1. Connection

- URL: `https://hawkeye.observer/mcp`
- Users connect to: **Universal URL** (everyone uses the same URL)

### 2. Tools

Synced from the server. Expect 17 tools, 8 read-only and 9 writes (`create_workflow` and `test_workflow` were added 2026-11-11 and declare `openWorldHint: true`, because their steps call other services), with no warnings about missing titles or annotations.

### 3. Listing

- **Server name** (≤ 100): `HawkEye`
- **One-liner** (≤ 200):

  > Check your websites, APIs, cron jobs and DNS monitoring from Claude: see what's down, review uptime and incidents, pause monitors and post status page updates.

- **Description** (≤ 2,000):

  > HawkEye monitors websites, APIs, scheduled jobs and DNS records for freelancers, agencies and small teams, and alerts them when something goes wrong. This connector brings your HawkEye account into Claude, so you can ask about your monitoring in plain language and act on it without switching tabs.
  >
  > Ask Claude what's down right now, how a client's site performed this month, or what happened during last night's outage, and it answers from your live monitors, uptime history and incidents. It can also list your public status pages and maintenance windows.
  >
  > With read and write access on a Pro or Agency plan, Claude can pause and resume monitors (one at a time or every monitor with a client's tag, for example during a deploy), add website, cron job and DNS monitors, set up and test multi-step API checks (such as signing in and then fetching an account; Claude names the secrets a check needs and you enter them in HawkEye, so it never sees a password), and schedule maintenance. Organization owners can also have Claude post updates on an incident, which appear on their public status pages. Claude always asks before anything public or destructive.
  >
  > You choose the organization and the access level when you connect, and you can disconnect at any time from your HawkEye account. The connector acts as you, with your permissions, and never sees passwords, billing, team settings or monitor credentials.
  >
  > Requires a HawkEye account (the free plan works for reading). Changes need Pro or Agency.

- **Categories** (1–5): choose the closest of the portal's options, such as Developer tools, Monitoring or observability, and Productivity.
- **Documentation URL:** `https://hawkeye.observer/docs/claude`
- **Privacy policy URL:** `https://hawkeye.observer/privacy`
- **Support contact:** the support address that receives contact-form mail (`CONTACT_TO_ADDRESS` in production)
- **Icon URL:** `https://hawkeye.observer/icon-192.png`
- **URL slug:** `hawkeye` (permanent once published)

### 4. Use cases

As submitted (each example works on the reviewer account's data):

1. **Check what's down right now.** See which monitors are failing, since when, and why, across websites, APIs, cron jobs and DNS records.
   > What's down in HawkEye right now, and how long has it been down?
2. **Review uptime for a site or client.** Uptime percentages, failed checks and average response times over the last day, week, month or year.
   > How was uptime for the Acme Bakery website over the last 30 days?
3. **Investigate an incident.** An incident's timeline, cause and the updates posted on it, and which public status pages show it.
   > Show me the latest incident on the hourly invoice export and the updates posted on it.
4. **Pause monitoring during a deploy or planned work.** Pause and resume monitors one at a time or by client tag, or schedule a maintenance
   window, so planned work doesn't trigger false alerts.
   > Pause all the Acme Bakery monitors while I deploy, then resume them when I say I'm done.
5. **Check that an API flow works, not just one address** (written 2026-11-11; not on the listing: the portal let us update the Description that day
   but has no way to edit use cases after submission, so adding it means asking mcp-review@anthropic.com). Claude writes the steps of a multi-step API check, saves it (it asks
   first), and runs it to show each step's result. Passwords and keys are named only; the check stays paused until you enter them in HawkEye.
   > Set up a check that signs in to api.example.com with our monitoring account and then fetches /account, and tell me which secrets to enter.
6. **Keep customers informed during an outage.** Post an update on an open incident that appears on your public status pages. Claude asks
   before publishing.
   > Post an update on the open incident saying we've found the cause and expect a fix within the hour.

**Prerequisites** (shown to reviewers):

> A HawkEye account (sign up free at https://hawkeye.observer/register; no credit card). Connecting opens HawkEye's sign-in and consent page, where
> you choose the organization and the access Claude gets. Read only works on every plan, including Free. Read and make changes (pausing and adding
> monitors, setting up multi-step API checks, scheduling maintenance, posting incident updates) needs a Pro or Agency plan. Posting incident updates and scheduling or deleting
> maintenance windows also need the organization's owner; members can read, pause, resume and add monitors. People who use two-factor sign-in
> confirm the connection with a code from their authenticator app. No other setup is needed. For review: the test account in the Test & launch
> step is the owner of an Agency organization with example data, and has two-factor sign-in turned off.

**Reads or writes:** both.

### 5. Company

HawkEye, `https://hawkeye.observer`, and the primary contact for review updates.

### 6. Authentication

- Authentication requirement: **Always required**
- Authentication method: **OAuth sign-in** → OAuth client: **Anthropic-held client credentials** → client ID from step 3 → **Anthropic holds a
  client secret** → the secret from step 3
- The portal marks **Installers bring their own OAuth client** as "Detected" (it found no dynamic client registration or Client ID Metadata
  Document in our metadata). Don't choose it: that mode means each customer creates an OAuth client in HawkEye, which they can't, and Anthropic
  arranges it by email. The secret choices "Not required" and "Installers provide a client secret" belong to that mode.

### 7. Data handling

- The API is **our own** (first party), on our domain.
- No personal health data. No sponsored content.

### 8. Test & launch

Reviewer instructions (fill in the account):

> In Claude, add a connector with the server URL **https://hawkeye.observer/mcp** (it's an MCP server address for Claude, not a web page; opening it in a browser shows an "UNAUTHENTICATED" error). When you connect, HawkEye's sign-in page opens: sign in with the test account below, choose the organization "<organization name>" and **Read and make changes**, and select **Allow**. You're then returned to Claude, connected.
>
> Email: directory-reviewer@hawkeye.observer
> Password: <password>
>
> The account is the owner of an Agency organization with example data: monitors for two example clients, a published status page (https://hawkeye.observer/status/hawkeye-demo), a weekly maintenance window, and a resolved incident with three updates on the "Hourly invoice export" monitor. Every tool can be tried on it. Posting an incident update makes it visible on the demo status page; please delete it afterward with delete_incident_update. Please don't change the password or turn on two-factor sign-in.

Confirm that every tool was run (step 5).

### 9. Compliance

Seven acknowledgments: the directory guidelines, first-party API use (ours), financial transactions (none: no tool moves money), AI media
generation (none), prompt injection (tool descriptions only describe the tool; untrusted text is returned as data, `docs/security.md#mcp-server`),
conversation data collection (none stored), and public documentation (`/docs/claude`).

## After the listing is live

**Status:** submitted 2026-11-08; published as a Community listing 2026-11-09: https://claude.ai/directory/hawkeye. Done on 2026-11-09: `/docs/claude` explains adding HawkEye from the
directory, the landing page has a "Works with Claude" card and FAQ answer, `llms.txt` says so, and Phase 3c is marked done in
`docs/plan-api-mcp.md`.

**Keep in place while listed:** the reviewer account (`directory-reviewer@hawkeye.observer`, Agency plan, no two-factor), its seeded data and the
two `hawkeye-demo-heartbeat` cron lines on the server, the Anthropic OAuth client, and `OAUTH_TOKEN_RATE_LIMIT_PER_MIN`. Anthropic re-checks
listed connectors; a reviewer who finds a broken account or a heartbeat that's down is a reason to delist. Tell Anthropic (through the portal)
before changing the server URL, the OAuth endpoints, tool names or what a tool does.

Original checklist:

- Update `/docs/claude` (`web/src/pages/docs/DocsPages.tsx`): replace "will be available once HawkEye is listed in Claude's connector directory"
  with how to add HawkEye from Claude's connector directory, and add a "Works with Claude" line to the landing page and `llms.txt` (feature
  sync checklist). Don't advertise it before it is listed.
- Mark Phase 3c done in `docs/plan-api-mcp.md`.
- The portal's connector dashboard shows server health and usage by tool; `hawkeye_mcp_tool_calls_total` shows the same from our side.
