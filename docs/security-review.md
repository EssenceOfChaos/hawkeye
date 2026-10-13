# Security review (Phase 8)

**Scope:** the whole repository as of Phase 8 (API, worker, frontend, deployment files). **Method:** manual review against the plan's §9 checklist, targeted tests for each control, dependency/vulnerability scanning, an audit of every place SQL is built and every log statement, and running the production images with real HTTPS/SMTP/Stripe-sandbox traffic.
This is a self-review by the developer of the system, not an independent penetration test; commission one before taking real customers.

## Findings fixed during the review

| Finding | Fix |
|---|---|
| Go stdlib/x-net vulnerabilities in 1.25.x | moved to Go 1.26.9; `govulncheck` reports 0 affecting |
| Tokens (password reset/verify) could leak via Caddy logs and Referer | log redaction, `no-referrer`, SPA strips token from URL |
| Sentry scrubber left the token in `Authorization: Bearer ...` | header pattern added + test |
| A panic was reported twice (recover and error handler) | marker error; test asserts exact count |
| Unknown `/api/v1/*` paths answered 401 to anonymous callers | explicit 404 + test |
| No limit on emails sent to one address (mail-bombing) | per-address hourly budget |
| No account deletion/export; Stripe customer would outlive the account | implemented; deletion cancels Stripe first |
| `SMTP_CA_FILE` declared but never read | parsed + test that every SMTP setting is read |
| Rate limits were per-process | shared PostgreSQL limiter |

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

1. **Privacy policy and Terms are placeholders** (`docs/legal-placeholders.md`). Blocker.
2. **`deploy/egress-filter.sh` is untested on Linux.** The application-level SSRF guard is tested, but run `scripts/verify-egress.sh` on the real host; without the firewall a bug in `safehttp` would be exploitable.
3. **No independent penetration test**; no formal audit log.
4. `govulncheck` lists one vulnerability in an unused `x/crypto/openpgp` package with no upstream fix; not reachable from our code, accepted.
5. Auth emails are sent synchronously by the API; a slow SMTP relay slows those requests (bounded by timeouts).
6. Per-account login ceiling can be used to briefly block someone's logins (see `docs/security.md`).
7. Stripe: refunds/disputes are manual; the webhook delivery hop (Stripe → our endpoint) needs verifying with `stripe listen` or a real endpoint. Rotate the sandbox keys that were pasted into chat, and use restricted keys in production.
8. SMTP was verified only against a fake server and Mailpit, not a real provider; send a test through yours and check SPF/DKIM/DMARC.
9. Single-host deployment is a single point of failure (see `docs/deployment.md`).
