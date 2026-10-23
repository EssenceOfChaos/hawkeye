# Security review (Phase 8)

**Scope:** the whole repository as of Phase 8 (API, worker, frontend, deployment files). **Method:** manual review against the plan's §9 checklist, targeted tests for each control, dependency/vulnerability scanning, an audit of every place SQL is built and every log statement, and running the production images with real HTTPS/SMTP/Stripe-sandbox traffic.
This is a self-review by the developer of the system, not an independent penetration test; commission one before taking real customers.

## Findings fixed during the review

| Finding | Fix |
|---|---|
| Go stdlib/x-net vulnerabilities in 1.25.x | moved to Go 1.26.10; `govulncheck` reports 0 affecting |
| Tokens (password reset/verify) could leak via Caddy logs and Referer | log redaction, `no-referrer`, SPA strips token from URL |
| Sentry scrubber left the token in `Authorization: Bearer ...` | header pattern added + test |
| A panic was reported twice (recover and error handler) | marker error; test asserts exact count |
| Unknown `/api/v1/*` paths answered 401 to anonymous callers | explicit 404 + test |
| No limit on emails sent to one address (mail-bombing) | per-address hourly budget |
| No account deletion/export; Stripe customer would outlive the account | implemented; deletion cancels Stripe first |
| `SMTP_CA_FILE` declared but never read | parsed + test that every SMTP setting is read |
| Rate limits were per-process | shared PostgreSQL limiter |
| API-check credentials stored in plain text (and in every backup) | AES-256-GCM at rest, bound to the monitor, key from `SECRETS_ENCRYPTION_KEY`, fail-closed; legacy values encrypted at API startup |
| API checks could be aimed at third-party APIs with state-changing methods by any new account, at 1-minute intervals | POST/PUT/PATCH/DELETE need a verified email and a 5-minute minimum interval |
| Deleted monitors kept credentials, headers and body for 30 days | wiped immediately on delete |

## Checklist (plan §9)

| Requirement | Where verified |
|---|---|
| Argon2id, constant-ish login behaviour, hashed single-use tokens | `internal/auth` tests |
| Cookie flags + CSRF | `internal/httpapi` tests, smoke checks |
| Tenant isolation on every resource | `TestOrganizationIsolation`, `TestMonitorTenantIsolationOverHTTP`, `TestTenantScoping` |
| Plan limits server-side, race-safe | `internal/plans`/monitor concurrency tests |
| SSRF: private IPs, redirects, DNS rebinding, ports, proxies | `internal/safehttp` tests; containerised worker recorded `blocked` for 169.254.169.254 and localhost |
| Webhook authenticity, idempotency, ordering | `internal/billing` tests, real-sandbox replay |
| Rate limiting / abuse | `internal/ratelimit`, `httpapi` tests |
| Input validation, body limits, timeouts | handler tests; `BodyLimit`, `ContextTimeout` |
| Secrets not in git/logs/images | `.gitignore`, Sentry/log scrubbers, image contents reviewed |
| Dependency scanning | CI `vulnerabilities` job |
| End-to-end MVP acceptance | `internal/e2e/mvp_test.go`, `scripts/smoke.sh` |

SQL: all queries are parameterised; the few places that assemble SQL (dynamic `UPDATE` column lists, retention table/batch) use fixed identifiers from code, never request data.

## Residual risks and launch blockers

1. **Privacy policy and Terms** are now written but have not had attorney review (`docs/legal-placeholders.md`). Blocker until reviewed.
2. **`deploy/egress-filter.sh` is untested on Linux.** The application-level SSRF guard is tested, but run `scripts/verify-egress.sh` on the real host; without the firewall a bug in `safehttp` would be exploitable.
3. **No independent penetration test**; no formal audit log.
4. `govulncheck` lists one vulnerability in an unused `x/crypto/openpgp` package with no upstream fix; not reachable from our code, accepted.
5. Auth emails are sent synchronously by the API; a slow SMTP relay slows those requests (bounded by timeouts).
6. Per-account login ceiling can be used to briefly block someone's logins (see `docs/security.md`).
7. Stripe: refunds/disputes are manual; the webhook delivery hop (Stripe → our endpoint) needs verifying with `stripe listen` or a real endpoint. Rotate the sandbox keys that were pasted into chat, and use restricted keys in production.
8. SMTP was verified only against a fake server and Mailpit, not a real provider; send a test through yours and check SPF/DKIM/DMARC.
9. Single-host deployment is a single point of failure (see `docs/deployment.md`).
10. Credential encryption uses one static key with no rotation tooling; losing it means customers must re-enter credentials. Custom headers and request bodies are not encrypted (documented and warned about in the UI and Terms).
11. A verified email is a modest barrier for state-changing API checks; a determined abuser can still verify an address. Watch for abuse and consider paid-plan gating if it appears.
12. **The free checkers (`POST /api/v1/tools/ssl-check` and `/tools/site-check`) make outbound connections from the API container**, on behalf of anonymous visitors. The application-level SSRF boundary (`safehttp`: hostnames only, port 443 only, every resolved address must be public, the validated address is the one dialed) is shared with the monitoring engine and tested, but `deploy/egress-filter.sh` only restricts the *worker* network, so the API container has no network-level backstop and can reach the database network. If you want one, run the checker in the worker network instead, or extend the firewall to the API's outbound traffic with an allow-list for PostgreSQL, SMTP and Stripe. Abuse limits: per IP 10/minute (`AUTH_RATE_LIMIT_PER_MIN`) and 40/hour, shared across API processes; per host 5 fresh checks per 10 minutes, 8 concurrent checks and a short result cache (60 s, 30 s for the website checker), **per API process and per tool** (`internal/toolguard`). The website checker is the heavier of the two, because it sends an HTTP request: it asks only for `GET /` of the host (the path and query a visitor types are discarded), follows at most 5 redirects through the same revalidating client as the monitors, returns only the status, timing and final address (no body, no query), and names itself in its User-Agent. **Decision: launch without a bot challenge.** The worst a distributed abuser can do is use up the checker's 8 slots (the tool then answers "busy"; monitoring is unaffected) or make us complete TLS handshakes with other people's servers at a modest rate. A challenge would cost sign-up conversions, add a third-party script (CSP and privacy-policy changes) and does not remove the SSRF boundary, which is the real control. Three alerts in `deploy/prometheus-alerts.yml` (`HawkEyeFreeCheckers…`, covering both tools) watch volume, refusals and clients hitting the per-IP limits; add a challenge (for example Cloudflare Turnstile) when one of them fires repeatedly. Logs: one `ssl check` or `site check` line per request (outcome and duration; the host only at debug level). The checkers do not persist what was checked (a host is held in memory for the length of the per-host window and cache, and appears in logs only at debug level). The cloud-init user data holds no secrets, so the Vultr metadata address (169.254.169.254, also blocked by `safehttp`) would not expose any.
13. **Production settings live in a third-party secret manager (Infisical)** (`docs/secrets.md`). Compromise of the Infisical account would expose every production setting (Stripe live keys, database password, `SECRETS_ENCRYPTION_KEY`, SMTP password): use a strong password and two-factor authentication on it, and keep the account's members to the people who need them. Each server holds a credentials file for a **read-only machine identity** (`~/.hawkeye-infisical.env`, mode 600, refused by the scripts if group- or world-readable); a stolen copy can read the production environment but cannot change anything, and is revoked by deleting its client secret. The settings are fetched only at deploy time and written to files on the server (mode 600), so the running service does not depend on Infisical, and an Infisical outage never blocks a release. Nothing secret is in git, in the infrastructure code or in cloud-init. A nightly encrypted copy of all settings goes to the backup bucket (age, the key that protects the database dumps), so losing the service is recoverable. The scripts never print values: they write files (mode 600) or hand the nightly copy straight to age, and print only counts and names the manifest knows. Not covered: the client secret travels to a new server over SSH from your laptop (`rebuild.sh create`), so that SSH path and the laptop are the trust anchors; and the real Infisical API has been exercised only by the fake used in the tests until the drill rebuild in `docs/secrets.md` step 8 has passed.
