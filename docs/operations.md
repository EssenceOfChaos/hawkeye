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

`deploy/prometheus-alerts.yml` contains 35 alert rules covering API errors and latency, panics, the shared limiter, security log writes, database pool pressure, workers not polling, scheduler lag,
queue backlog/backpressure, missed checks, dropped jobs, mass check failures (a sign of **our** network problem), stale retention, alert-email backlog/failures, billing
failures/risk events, the free checkers (heavy use, people turned away, clients hitting their limits), and the alerting itself (below). A unit test fails if a rule names a metric
the code does not export, and `scripts/check-alerting.sh` (run by CI) validates the rules with `promtool`, runs the rule tests in `deploy/prometheus/alerting_test.yml`, and checks the
Alertmanager configuration with every combination of settings.

### How an alert reaches you

```
api :9091, worker :9090 ──scrape──▶ Prometheus ──fires──▶ Alertmanager ──▶ Telegram   page: with sound, again every 5 min until it resolves or is silenced
                                    (rules)                     │                      ticket: silent, again every 12 h
                                                                └──Watchdog, ~every minute──▶ healthchecks.io ──(its own channels)──▶ you
server (cron, every minute) ──/readyz answers?──▶ healthchecks.io "HawkEye" check (scripts/heartbeat.sh)
```

`scripts/deploy.sh` runs Prometheus and Alertmanager (`deploy/docker-compose.monitoring.yml`) next to the app; nothing is published on the host. Each failure has a path that does
not depend on the thing that failed:

| What breaks | Who tells you |
|---|---|
| Something inside the app (the 31 app rules) | Alertmanager, on Telegram |
| The worker's or API's metrics stop answering | `HawkEyeMetricsUnreachable` / `HawkEyeMetricsMissing`, on Telegram |
| Prometheus or Alertmanager stops, or Telegram keeps refusing our messages | The Watchdog pings stop; healthchecks.io alerts you |
| The whole server, the site or the database | `scripts/heartbeat.sh` pings stop or report a failure; healthchecks.io alerts you |
| A regional worker host | Its own healthchecks.io check (`docs/deployment.md#regional-worker-hosts`) |

**Set it up** (once; each step is a setting in Infisical, applied by the next deploy or `scripts/deploy.sh secrets-only`):

1. **A Telegram bot and chat for our alerts.** Create a bot with @BotFather and put its token in `ALERTS_TELEGRAM_BOT_TOKEN`. A separate bot is best: the customer-facing bot
   (`TELEGRAM_BOT_TOKEN`, which is used when this is empty) shares Telegram's rate limits with every customer's alerts. Send the bot a message, then read your chat id from
   `https://api.telegram.org/bot<token>/getUpdates` (`"chat":{"id":…}`; a group's id is negative) and put it in `ALERTS_TELEGRAM_CHAT_ID`.
2. **Let pages wake you.** Telegram does not repeat a message until it is acknowledged, so Alertmanager does: a page is sent again every 5 minutes while it lasts. On your
   phone, allow notifications from this chat through Do Not Disturb or Focus (iOS: add Telegram to the Focus's allowed apps; Android: mark the chat's notifications as priority).
   Tickets are sent silently and never need this.
3. **The watchdog.** Create a healthchecks.io check named "HawkEye alerting" with a period of 2 minutes and a grace time of 8 minutes, and put its ping URL in
   `ALERTS_WATCHDOG_URL`. With `HEALTHCHECKS_API_KEY` set you can create it from the server instead:
   `python3 scripts/regions.py healthcheck "HawkEye alerting" 120 480` (prints the URL). In healthchecks.io, add a **Telegram integration** (its own bot, independent of ours)
   and a second channel such as email or the mobile app, and make sure it is attached to this check, the "HawkEye" heartbeat check and the regional worker checks.
4. **The egress filter** lets Prometheus (fixed address 172.30.10.6) read the worker's metrics on the worker network. A server whose filter was applied before this change
   needs it reapplied once: `sudo systemctl restart hawkeye-egress`, then `scripts/verify-egress.sh`.
5. **Prove it:** `scripts/alerts.sh targets` (every target `up`), `scripts/alerts.sh test page` (your phone rings within about 30 seconds), and in healthchecks.io the
   "HawkEye alerting" check shows a ping every minute or two. Repeat the test page after changing the bot, the chat or your phone.

**When a page arrives:** follow the runbook link in the message. `scripts/alerts.sh silence <AlertName> 2h` acknowledges it (the repeats stop; it still resolves normally), and
`scripts/alerts.sh list` shows what is firing. Prometheus reads `deploy/prometheus-alerts.yml` only at start, so each release restarts it and Alertmanager; after changing rules any other way, run `docker restart hawkeye-prometheus-1`. Alertmanager's log (`docker logs hawkeye-alertmanager-1`) says at startup where alerts go, and warns when a setting is missing.

Without these settings the stack still runs and evaluates every rule, but nothing is delivered, so self-hosted installs can opt out by leaving them empty or by removing
`deploy/docker-compose.monitoring.yml` from the Compose command. Optional: set `SENTRY_DSN` for error tracking (events are scrubbed of emails, tokens and keys before leaving the process). See *Sentry shows nothing* below to check it.

## HawkEye's status page (status.hawkeye.observer)

Customers check `https://status.hawkeye.observer` when they think HawkEye is down, so it must not run on HawkEye's server. It is a Cloudflare Worker
(`status/`) with its own database (Cloudflare D1, `hawkeye-status`). Every minute it loads `https://hawkeye.observer/` and
`/api/v1/plans` from Cloudflare, and reads the newest heartbeat from the primary worker (`internal/statusbeat`), which reports how overdue the oldest
queued check is. A component is down after two failures in a row, counted from the first; "Monitoring and alerts" is down when heartbeats stop for 3
minutes or report checks more than 5 minutes overdue. Uptime is computed from those outages, from the day measuring began; nothing before it is
counted or claimed. Raw check results are kept 14 days, outages and incidents for good.

**Settings.** Cloudflare: the `DB` binding and the route are in `status/wrangler.toml`; the secrets `HEARTBEAT_TOKEN` and `ADMIN_TOKEN` are set with
`wrangler secret put`. Infisical: `STATUS_HEARTBEAT_URL` (`https://status.hawkeye.observer/heartbeat`) and `STATUS_HEARTBEAT_TOKEN` (the same value as
`HEARTBEAT_TOKEN`) in `prod`; `ADMIN_TOKEN`, `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` in `staging`, which no server reads, because anything in
`prod` is copied to the server.

**First deploy** (from a laptop). Keep two files, mode 600, outside the repository: `~/.hawkeye-secrets/status-deploy.env` with
`CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` (who may deploy), and `~/.hawkeye-secrets/status-worker.env` with `HEARTBEAT_TOKEN` and
`ADMIN_TOKEN` (the Worker's own secrets). Keep them apart: everything in the second file is uploaded to the Worker.

```sh
cd status && npm ci
set -a; . ~/.hawkeye-secrets/status-deploy.env; set +a
npm run migrate                                                       # creates the tables in D1
npx wrangler deploy --secrets-file ~/.hawkeye-secrets/status-worker.env   # the Worker, its secrets, and status.hawkeye.observer (DNS and certificate)
```

The secrets go up with the first version, so the page never runs without them (heartbeats would be refused and the history would start with a false
outage). Later deploys can leave out `--secrets-file`: secrets are kept between versions.

Then add the two `STATUS_HEARTBEAT_*` settings to `prod` in Infisical and run `scripts/deploy.sh secrets-only` (or a normal release); the worker starts
sending heartbeats and "Monitoring and alerts" turns green within a minute. Later changes to `status/`: `npm run deploy` again (and `npm run migrate`
when a migration was added), or connect the repository in Cloudflare (Workers Builds, root directory `status`) to deploy on every push to `main`. CI
type-checks and tests it either way.

**Posting an incident.** Write for customers: what they notice, not what broke inside. Status is `investigating`, `identified`, `monitoring` or
`resolved` (which closes it). The page shows it within a minute.

```sh
S=https://status.hawkeye.observer; T="Authorization: Bearer $ADMIN_TOKEN"
curl -s -X POST $S/api/incidents -H "$T" -H 'Content-Type: application/json' \
  -d '{"title":"Alerts are delayed","status":"investigating","message":"Some alert emails are arriving late. Checks are running normally."}'
# → {"id":4}
curl -s -X POST $S/api/incidents/4/updates -H "$T" -H 'Content-Type: application/json' \
  -d '{"status":"resolved","message":"Delivery is back to normal. Delayed alerts were all sent."}'
```

Outages themselves are recorded automatically; an incident note is for explaining one. If a check was wrong (Cloudflare could not reach us but
customers could), say so in a note rather than deleting the outage: the numbers stay honest. To look at raw results:
`npx wrangler d1 execute hawkeye-status --remote --command "SELECT * FROM checks ORDER BY at DESC LIMIT 20"`.

## Runbooks

Each section is linked from an alert in `deploy/prometheus-alerts.yml`.

### API errors

1. `docker compose logs --tail 200 api` (JSON; every request has a `request_id`, which is also in the `X-Request-Id` response header and in error-tracker events).
2. 5xx on one route only → look at that route's handler/logs. 5xx everywhere → check `/readyz` (database) and `hawkeye_db_pool_*` (pool exhausted? raise `DB_MAX_CONNS`, check for slow queries with `SELECT * FROM pg_stat_activity WHERE state <> 'idle' ORDER BY query_start`).
3. Latency up with CPU low → database. Latency up with CPU high on the API container → scale `api` replicas (they are stateless; sessions and rate limits are in PostgreSQL).
4. A burst of 429s: see `hawkeye_rate_limited_total` by scope; legitimate NAT'd offices can hit the per-IP limits (raise `AUTH_RATE_LIMIT_PER_MIN` / `GLOBAL_RATE_LIMIT_PER_MIN`).

### Free tools abuse

Symptoms: `HawkEyeFreeCheckersHeavyUse`, `HawkEyeFreeCheckersTurningPeopleAway` or `HawkEyeFreeCheckersClientsThrottled`. The SSL and website checkers (`POST /api/v1/tools/ssl-check` and `/tools/site-check`) are public and connect to hosts visitors name.

1. Look at the API log: one `ssl check` or `site check` line per request with the outcome (`up/ok`, `HOST_RATE_LIMITED`, `BUSY`, ...) and duration. Hosts are logged only at debug level (`LOG_LEVEL=debug`), which also shows which hosts are being asked about; client addresses are in the request log.
2. A spike of ordinary outcomes from many addresses is probably a link going around (or a crawler): nothing to do. Mostly `BUSY` or `RATE_LIMITED` from a few addresses is a script. The per-IP limits (10/minute, 40/hour) and per-host limit (5 fresh checks per 10 minutes) already contain it; a determined distributed abuser can still keep the 8 check slots busy, which affects only these two tools.
3. To stop it at once: set `DISABLE_FREE_TOOLS=true` and restart the API; both endpoints then answer 503 and the pages say the tool is unavailable. Monitoring is unaffected.
4. To block an address, do it at the host firewall (`ufw deny from <ip>`) or in Caddy. If it keeps recurring, add a bot challenge to the two forms (for example Cloudflare Turnstile; the CSP in `deploy/Caddyfile` needs the script and frame origin) and verify the token in `handlers_tools.go` before calling the checker (`docs/security-review.md`, item 12).
5. A site owner who complains about requests from us: the requests are `GET /` with `User-Agent: HawkEye-StatusChecker/1.0 (+https://hawkeye.observer/tools/is-website-down)`, at most 5 per host per 10 minutes. Tell them they can block that user agent.

### API key abuse

API keys (`Authorization: Bearer hk_…`) let scripts use the API as a person in one organization (`docs/api.md#api-keys`).

1. `hawkeye_rate_limited_total{scope="token"}` and `{scope="token-write"}` count requests refused by the per-key limits (120 a minute, 30 writes
   a minute). The request log shows the route and status; a key is never logged, but its owner can see its prefix and when it was last used on
   the API keys page.
2. To stop one key: the person (or the organization's owner) revokes it on the API keys page. To do it for them, in SQL:
   `UPDATE api_tokens SET revoked_at = now() WHERE prefix = 'hk_…' AND revoked_at IS NULL;` (the prefix is the first 10 characters).
3. To stop all keys at once: set `DISABLE_API_KEYS=true` and restart the API. Every request made with a key answers 503 `API_KEYS_DISABLED`;
   browser sessions and the app are unaffected, and the API keys page says keys are turned off. Keys work again when it is unset.
4. Expiry emails: the worker emails people 7 days before a key expires and when it does (`apitokens.ExpiryJob`, every 15 minutes, on the primary
   worker only). A missing email is in the worker log as `send API key expiry email failed`.

### OAuth clients (connecting Claude)

Clients that may connect to the MCP server by signing in are registered by hand. The production database is reachable only inside the server's
Docker network, so run the command on the server, from the API image (it is built in next to `migrate`), in the deploy folder:

```sh
cd ~/hawkeye
docker compose --env-file .env.production -f deploy/docker-compose.prod.yml -f deploy/docker-compose.local-db.yml \
  run --rm --entrypoint /usr/local/bin/oauthclient migrate create -name Claude -redirect https://claude.ai/api/mcp/auth_callback
# … the same with `list`, or `disable <client-id>` (stops it and revokes every connection it holds)
```

Use the same Compose files as `scripts/deploy.sh`: production runs its database from `docker-compose.local-db.yml` (leave it out with a managed
database).

**Throttling.** Every Claude user's token requests reach `/oauth/token` from Anthropic's servers (`160.79.104.0/21`), so the per-IP limit there
(`OAUTH_TOKEN_RATE_LIMIT_PER_MIN`, default 1,200) is in effect a ceiling on all of Claude's token traffic. Access tokens last an hour, so it
covers tens of thousands of connected people. If `HawkEyeOAuthTokenThrottled` fires (`hawkeye_rate_limited_total{scope="oauth-token"}`), raise it
and redeploy; codes and refresh tokens are 256-bit random values, so the limit only guards against floods.

Locally, against a development database: `DATABASE_URL=... go run ./cmd/oauthclient create …`.

`create` prints the client ID and secret once; enter both in Anthropic's directory portal (Authentication step), which holds them for Claude.
Claude's web, desktop and mobile apps all use that one redirect URI (claude.com/docs/connectors/building/authentication, checked
2026-11-08). Claude Code doesn't use this client: it identifies itself with a Client ID Metadata Document on a loopback redirect, which HawkEye
doesn't support yet, so Claude Code users connect with an API key instead (`/docs/claude`). The worker deletes used and expired codes and
refresh tokens hourly, and connections revoked over 30 days ago. `DISABLE_API_KEYS` also stops OAuth (its tokens are API tokens).

### MCP server

`/mcp` serves Claude Code and other MCP clients with the same API keys (`docs/api.md#mcp-server`). `hawkeye_mcp_tool_calls_total{tool, outcome}`
counts tool calls (`ok`, `refused` for permission, plan, input and budget refusals, `error` for failures on our side) and
`hawkeye_mcp_tool_duration_seconds{tool}` their latency; an `error` outcome is also logged as `MCP tool failed` with the tool and organization.
The per-key limits and `DISABLE_API_KEYS` (see above) apply to it too.

### Security log

Owners read their organization's log under **Security log** in the account menu, and everyone reads their own sign-ins on the Account page
(`docs/security.md#security-log`). For support and investigations, read it on the server:

```
docker exec hawkeye-db-1 psql -U hawkeye -d monitoring -c "
  SELECT occurred_at, action, actor_type, actor_email, credential_name, target_type, target_name, details, host(ip)
  FROM audit_events WHERE organization_id = '<org id>' ORDER BY id DESC LIMIT 50"
```

Use `account_id = '<user id>'` for a person's own log, `credential_id = '<api key id>'` for everything one key (or, for a connected app, one grant) did,
and `request_id` to match an event with the API's request log line. Rows can't be changed (`audit_events is append-only`); they are removed after a year.

### Security log writes failing

`HawkEyeSecurityLogWritesFailing`: changes are succeeding but some are missing from the log. The API logs `could not write to the security log`
with the action and the error. The usual causes are the database refusing writes (disk full, connections exhausted: see *Database growing* and
`HawkEyeDbPoolSaturated`), or a release that records an action the database's `action` check or the `audit` package doesn't know (a bug: the error
names the action). Nothing needs replaying; once writes succeed again new events are recorded. Note the window in case someone asks what happened in it.

### Locked out: lost authenticator app and recovery codes

Two-factor sign-in has no email recovery, on purpose. If someone has lost both their phone and their recovery codes, confirm who they are first
(they can reset their password through email, which proves the mailbox; also ask for something only the account holder knows, such as their plan,
recent invoices or monitor names). Then turn it off for them:

```sql
BEGIN;
UPDATE users SET totp_secret = NULL, totp_pending_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE email = '<address>';
DELETE FROM recovery_codes WHERE user_id = (SELECT id FROM users WHERE email = '<address>');
COMMIT;
```

If they own an organization that requires two-factor sign-in, they set it up again the next time they open it. Ask them to turn it back on.

### Settings were not refreshed (secret manager)

Symptoms: the deploy log says `the secret manager is unreachable`, `rejected our credentials`, or `the secrets are not acceptable`; or a change made in Infisical did not reach the server.
Run `scripts/sync-secrets.sh --check` on the VM: it fetches and validates without writing anything and says which. `unreachable` → Infisical or the network is down; releases carry on with the
settings already on the server, so nothing is urgent. `rejected our credentials` → the machine identity's client secret was revoked or rotated (or its role no longer reads `prod`): put a valid
one in `~/.hawkeye-secrets/.hawkeye-infisical.env` and run `scripts/rebuild.sh credentials --host <ip>`. `not acceptable` → it names the settings (never the values): fix them in Infisical.
To apply a change now: `ssh deploy@<ip> 'cd hawkeye && scripts/deploy.sh secrets-only'` (or the Deploy workflow with "Only apply changed settings"). Details: `docs/secrets.md`.

### Email delivery failing

Symptoms: `HawkEyeAlertEmailBacklog`/`HawkEyeAlertEmailFailing`. Delivery errors are in `notification_deliveries.last_error` (sanitized) and the worker log.
`smtp auth` errors → wrong `SMTP_USERNAME/PASSWORD` (rotated key?); `starttls`/certificate errors → wrong `SMTP_TLS` mode/port or a private CA (`SMTP_CA_FILE`); `451/452` → provider throttling (retried automatically with backoff);
`550/553` → bad recipient (marked failed, no retry). Deliveries retry for hours, so fixing the credential lets queued alerts go out; to push them now:
`UPDATE notification_deliveries SET next_attempt_at = now() WHERE status = 'pending';`. Account emails (verification, reset) are sent by the API process; their failures are logged as `send ... email failed`.

### Onboarding (lifecycle) emails

New accounts that verify their address but never add a monitor get up to three emails, 1, 3 and 7 days after verifying (`internal/lifecycle`). They are decided from the data at send time (no stage column, nothing scheduled), so someone who adds a monitor simply stops matching. Anyone who unsubscribed, has a disabled account, or verified more than 10 days ago is never mailed, so switching this on does not message old accounts.

**Turning it on.** It is off until `LIFECYCLE_FROM_ADDRESS` is set on the worker (for example `HawkEye <customer_support@hawkeye.observer>`). Before that: verify the sender's domain with the email provider (Resend: add the domain, create its DNS records, wait for _Verified_), and make sure `LIFECYCLE_REPLY_TO` (default: the From address) is a mailbox someone reads, because the 7-day email asks people to reply. To keep alert deliverability separate from engagement-driven mail, use a different domain or subdomain than `EMAIL_FROM_ADDRESS`. `LIFECYCLE_POSTAL_ADDRESS` adds a postal address to the footer if you decide you need one. Turn it off again with `DISABLE_LIFECYCLE_EMAILS=true` or by emptying `LIFECYCLE_FROM_ADDRESS`; nothing already sent is affected.

**Sending behavior.** Passes run every `LIFECYCLE_INTERVAL` (10m), at most 20 people per email per pass, one send per second (so a batch cannot use the provider's rate limit that alerts need), and never two of these emails within 48 hours to the same person. A temporary send failure ends the pass and is retried next pass; a permanent refusal (bad address) is recorded and not retried. Metric: `hawkeye_lifecycle_emails_total{kind,result}` (`sent|refused|failed`). Every email carries `List-Unsubscribe` / `List-Unsubscribe-Post` headers (one-click, `POST /api/v1/lifecycle/unsubscribe?token=...`, no session or CSRF token) and a footer link to `/unsubscribe`. Unsubscribing sets `users.lifecycle_opt_out_at` and affects only these emails.

**Measuring it.** Judge by what people do, not opens. Share of each signup week that added a first monitor within 7 days of verifying:

```sql
SELECT date_trunc('week', u.email_verified_at) AS week,
       count(*) AS verified,
       count(*) FILTER (WHERE EXISTS (SELECT 1 FROM organization_members om JOIN monitors m ON m.organization_id = om.organization_id
                                      WHERE om.user_id = u.id AND m.created_at < u.email_verified_at + interval '7 days')) AS activated
FROM users u WHERE u.email_verified_at IS NOT NULL GROUP BY 1 ORDER BY 1 DESC;
```

And who added a monitor after each email: join `lifecycle_emails` (`user_id`, `kind`, `sent_at`) to the first `monitors.created_at` of the user's organizations. Links in the emails carry `utm_campaign=<kind>`, so the analytics landing report shows the same split.

### Weekly summary email

Every Monday each verified member of an organization that has monitors gets a summary of the previous week (Monday to Monday, UTC): overall and per-site uptime (least available first, ten listed), incidents and downtime, the slowest sites, and certificates and domains expiring within 30 days (`internal/weeklysummary`). **On by default**; each person turns it off under Account → Email preferences or with the email's link (`/unsubscribe?token=…&list=weekly_summary`, which stops only this email). Someone in two organizations with monitors gets one email for each.

**Sending behavior.** Passes run every `WEEKLY_SUMMARY_INTERVAL` (15m) but send only from Monday 07:00 UTC until Wednesday 24:00 UTC: a worker that was down on Monday catches up, and nobody gets last week's summary at the end of the week. At most 200 per pass, one per second. A row in `weekly_summaries` (person, organization, week) is claimed before each send, so with several workers each email goes at most once; a temporary failure releases the claim and ends the pass, a permanent refusal is not retried. Sent from `LIFECYCLE_FROM_ADDRESS` (replies to `LIFECYCLE_REPLY_TO`) when set, so this bulk mail never affects the alert sender's reputation; otherwise from `EMAIL_FROM_ADDRESS`. `DISABLE_WEEKLY_SUMMARY=true` turns it off on a worker. Metric: `hawkeye_lifecycle_emails_total{kind="weekly_summary"}`.

**Who turned it off:**

```sql
SELECT count(*) FILTER (WHERE weekly_summary_opt_out_at IS NULL) AS receiving, count(*) FILTER (WHERE weekly_summary_opt_out_at IS NOT NULL) AS opted_out
FROM users WHERE email_verified_at IS NOT NULL;
```

Links in the email carry `utm_campaign=weekly_summary`.

### Feature waitlists

"Get notified" sign-ups for coming-soon features (`internal/waitlist`, [API](api.md#feature-waitlists-get-notified)). Only confirmed sign-ups count; unconfirmed ones are dropped by the hourly clean-up once their 48-hour link expires.

```sql
SELECT feature, count(*) FILTER (WHERE confirmed_at IS NOT NULL) AS confirmed, count(*) FILTER (WHERE confirmed_at IS NULL) AS pending,
       count(*) FILTER (WHERE user_id IS NOT NULL) AS customers
FROM waitlist_entries GROUP BY feature ORDER BY confirmed DESC;
```

**Announcing a launch.** Email each confirmed address for that feature once, including its own removal link (`<APP_BASE_URL>/waitlist/leave?token=<remove_token>`), then delete that feature's rows: the address was given for that one email and nothing else.

```sql
SELECT email, remove_token FROM waitlist_entries WHERE feature = 'api_workflows' AND confirmed_at IS NOT NULL;
-- after sending:
DELETE FROM waitlist_entries WHERE feature = 'api_workflows';
```

**Adding or removing a feature** takes a migration (the table's `CHECK`), `waitlist.Features`, and the cards in `web/src/components/ComingSoon.tsx`. Remove a feature's card when it launches.

### Monthly PDF reports

Pro and Agency can download a PDF uptime report for any website (Reports page, or the monitor's page): uptime, confirmed downtime, a day-by-day chart and the incident list for a calendar month in UTC. The PDF is drawn in Go (`internal/reportpdf`, the `go-pdf/fpdf` library), so there is no browser or other program to run, and a report is generated when asked for and never stored. A month is refused if it is older than the plan's history (its results would be partly deleted and the report wrong), and the month in progress is a "so far" report. Agency can add a name and a logo (`organization_branding`); the logo is validated and re-encoded on upload (PNG or JPEG only, 512 KB, 2000 x 2000 pixels, never SVG), and is printed only while the plan includes branding: after a downgrade it is kept but the reports go back to the HawkEye look.

**Monthly email.** An organization that turns it on (Reports page) gets, on the first pass after the month ends (an hour after midnight UTC, then hourly until sent), one email per owner with one PDF per website that had checks that month, 20 PDFs per email (larger accounts get "part 1 of 3" and so on). It is sent by the worker through the normal sender (`EMAIL_FROM_ADDRESS`) and only to verified owners; the owner forwards each PDF to the client. Nothing is ever sent to the agency's clients. Switch off with `DISABLE_MONTHLY_REPORTS=true`; `MONTHLY_REPORT_INTERVAL` (default 1h) is how often a pass runs. Metric: `hawkeye_monthly_report_emails_total{result}` (`sent|refused|failed`).

**Safe to repeat.** `monthly_report_emails` records each owner's email before it is sent (so nobody gets one twice, even with several workers or after a crash), and `monthly_report_runs` marks a month finished. A temporary mail failure releases the record and the next pass retries; a refused address is recorded and not retried. An organization whose plan no longer includes reports is skipped without being marked, so upgrading later in the month still sends that month's reports.

**Known limits.** Text is printed in the Western European character set of the standard PDF fonts: a site named in Cyrillic or Chinese prints with replacement marks (its PDF still works). Reports use UTC months, not the client's time zone. Re-sending a month already sent means deleting its `monthly_report_runs` and `monthly_report_emails` rows.

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
The one thing sent on purpose, errors or not, is a **start notice**, and it goes to Sentry's **logs**, not to Issues, so it raises no alert and does not use the error quota. Each time the API and the worker start, each logs `Hawkeye version <APP_VERSION> is being deployed at <UTC time>` at the info level, with the attributes `event=start`, `component=api|worker` and `version` (the commit `scripts/deploy.sh` deployed). Find it under **Explore > Logs** in Sentry; search `event:start`, or filter on `component` or `version`. It is how a deploy, a rollback or a restart shows up, and it proves that both processes can reach Sentry. Logs are not grouped by version (that is what Issues do), so read them as a timeline. Like errors they are scrubbed, and the machine's host name is removed. Because it is sent on every start it also appears for restarts that are not releases (a server reboot, `deploy.sh secrets-only`, a crash loop). If a deploy leaves no start notice in Logs, error tracking is off or unreachable (follow the steps below), or Logs is not enabled for the Sentry project or plan.
To check the setup, with the compose command used by `scripts/deploy.sh`:

1. `... logs api worker | grep "error tracking"`: `error tracking enabled sentry_host=… sentry_project=…` shows where events go (the project number must be the one you are looking at in sentry.io); `error tracking disabled` means `SENTRY_DSN` is empty in `.env.production`; `does not look like a DSN` means it is mangled. The key part of the DSN is never logged.
2. `... exec api /usr/local/bin/api sentry-test` sends one labeled test event and says whether Sentry accepted it. It exits 0 only when it was delivered, and prints the SDK's complaint otherwise (a wrong key or a deleted project gives a refusal, a blocked network a timeout). The event appears under Issues, tagged `component=sentry-test`, within a minute.
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
`pending` with a future `next_attempt_at` is a retry waiting for backoff; `failed` has the sanitized reason in `last_error`.
(3) Is a worker running (the dispatcher lives in `cmd/worker`)? In development the email provider is `console`, so mail appears in the worker log, not a mailbox.
To retry a failed delivery now: `UPDATE notification_deliveries SET status='pending', attempt_count=0, next_attempt_at=now() WHERE id='…';`

These are customers' alerts. For our own alerts not reaching Telegram, see *Metrics unreachable* below and `docker logs hawkeye-alertmanager-1`.

### Metrics unreachable

`HawkEyeMetricsUnreachable`, `HawkEyeMetricsMissing` or `HawkEyeAlertRulesFailing`: some alert rules can no longer fire, so treat this as a blind spot, not noise.

1. `scripts/alerts.sh targets` shows each target and the last scrape error.
2. `worker` times out or is refused: the egress filter is probably blocking Prometheus. `sudo deploy/egress-filter.sh status` must show a rule with `-s 172.30.10.6/32` and
   `--dport 9090`; if not, `sudo systemctl restart hawkeye-egress`. Also check that `docker inspect hawkeye-prometheus-1` shows 172.30.10.6 on the `hawkeye_checks` network.
3. `HawkEyeMetricsMissing` for `api` or `worker`: that container is not running (`docker compose ps`), or it is not on the network Prometheus looks on (`backend` for the API,
   `checks` for the worker).
4. `HawkEyeAlertRulesFailing`: `docker logs hawkeye-prometheus-1 | grep -i "rule"` names the rule; fix it in `deploy/prometheus-alerts.yml` and run `scripts/check-alerting.sh`.
5. healthchecks.io says "HawkEye alerting" is down: Prometheus or Alertmanager is not running (`docker compose ps`), or Telegram keeps refusing our messages (a revoked bot
   token, or the bot was removed from the chat). `docker logs hawkeye-alertmanager-1 | grep -i telegram` shows Telegram's answer. Fix the setting, deploy `secrets-only`, then
   `scripts/alerts.sh test page`.

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

### A DNS monitor fails but the record looks right

DNS monitors ask the worker's resolver, so a recent change shows up only once the old record's TTL expires there: compare with `dig <name> <type> @<resolver>`, using the resolver the worker uses. Other common causes are a TXT value in different case (TXT comparison is case-sensitive) and a `blocked` result, which means the name resolves to a non-public address. The answers are deliberately not stored, so the check history cannot show what was returned. Rising `dns` failures across many DNS monitors point at the resolver, not at customers: see *Many dns or timeout failures across unrelated sites*.

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
  inconclusive canary (canceled, or no endpoints configured) counts as healthy.

### Wipe a poisoned queue

(safe: the scheduler re-enqueues due monitors): `DELETE FROM check_jobs;`

## Tuning

`JOB_LEASE` must exceed the longest check: 30 s max timeout plus connection setup. Raising `FAILURE_THRESHOLD` reduces false alarms and delays detection by one interval per step.
