[![CI](https://github.com/EssenceOfChaos/hawkeye/actions/workflows/ci.yml/badge.svg)](https://github.com/EssenceOfChaos/hawkeye/actions/workflows/ci.yml)

# Hawkeye

Multi-tenant website and API monitoring SaaS: add websites and APIs, detect downtime, get alerts, review uptime.
Go + Echo REST API, PostgreSQL, React + TypeScript frontend.

**Status:** all 8 phases complete: auth and tenancy, monitors with plan limits, the monitoring engine (durable scheduler/queue, SSRF-safe checks), incidents and email alerts, dashboard/uptime/charts/retention, Stripe billing (verified against the Stripe sandbox; real webhook delivery still needs `stripe listen`, see `docs/billing.md`), and production readiness (hardened images, HTTPS, backups, monitoring, security review). It also checks APIs (custom methods, headers, body, authentication, response assertions; credentials encrypted at rest) and warns before HTTPS certificates expire (30, 14, 7 and 1 days before, and when expired) and before domain registrations do (RDAP with a WHOIS fallback, from monitor URLs or added by hand; limits per plan). Alerts go to email, Slack incoming webhooks and signed webhooks; Pro and Agency can schedule maintenance windows that suppress incidents and alerts; every organization can publish a public status page at `/status/<slug>`; paid plans can check a monitor from several locations (`CHECK_REGIONS`, see `docs/deployment.md`), raising an incident only when most locations agree. **Free tools** at `/tools` bring in search traffic and point visitors to sign-up: an SSL certificate checker, an "is my website down?" checker, and uptime SLA and downtime cost calculators. The two checkers connect out to hosts visitors name, so they have their own abuse limits (`docs/security.md`) and a kill switch (`DISABLE_FREE_TOOLS`). Every public page has its own title, description, link-preview image and sitemap entry, written at build time (`docs/architecture.md`). **Settings and secrets** live in Infisical and are fetched onto each server at deploy time (`docs/secrets.md`); a new server needs one small credentials file and nothing is copied by hand. **Before real customers:** have a lawyer review the Privacy Policy and Terms (`docs/legal-placeholders.md`), set `SECRETS_ENCRYPTION_KEY`, and read the residual risks in `docs/security-review.md`.
See `docs/architecture.md` for the design and `docs/deployment.md` to deploy.

## Prerequisites

Go (see `go.mod`; newer toolchains are downloaded automatically), Node 22+, and either Docker or a local PostgreSQL 15+.

## Quick start (Docker)

```sh
make up        # PostgreSQL, migrations, API on :8080, worker (metrics :9090), web on :3000
```

## Quick start (local processes)

```sh
cp .env.example .env
make db-up                 # PostgreSQL in Docker (or point DATABASE_URL at your own server)
make migrate               # apply migrations
make run-api               # http://localhost:8080
make run-worker            # scheduler + check workers (metrics on :9090)
make web-install && make web-dev   # http://localhost:3000 (proxies API calls to :8080)
```

Register from the command line (dev emails are printed in the API log):
`curl -c jar -b jar localhost:8080/api/v1/auth/me` then POST with `-H "X-CSRF-Token: <csrf_token cookie>"`.

Check it: `curl localhost:9090/metrics` (worker) and `curl localhost:8080/healthz` (liveness) and `curl localhost:8080/readyz` (readiness; verifies PostgreSQL).

## Tests and quality gates

```sh
createdb monitoring_test   # server where test databases get created
export TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/monitoring_test?sslmode=disable'
make check                 # gofmt, go vet, go test -race, web lint/test/build
```

Without `TEST_DATABASE_URL`, the PostgreSQL integration tests are **skipped** (the test log says so). CI always sets it.
Each integration test creates and drops its own temporary database on that server, so the role needs CREATEDB.
API reference: `docs/api.md`. Security notes: `docs/security.md`. Running the engine: `docs/operations.md`. Brand assets: `docs/branding.md`. Billing/Stripe: `docs/billing.md`. Deployment: `docs/deployment.md`. Settings and secrets: `docs/secrets.md`. Backups: `docs/backup-restore.md`. Security review: `docs/security-review.md`.

## Commands

| Command                                  | Purpose                                                                                     |
| ---------------------------------------- | ------------------------------------------------------------------------------------------- |
| `make migrate` / `migrate-down`          | Apply / revert one migration (`cmd/migrate`)                                                |
| `make run-api`                           | Run the API                                                                                 |
| `make run-worker`                        | Run the monitoring engine (scheduler + workers + `/metrics`)                                |
| `make test`, `make test-race`            | Go tests                                                                                    |
| `make lint`                              | gofmt, vet, web lint                                                                        |
| `make build`                             | Build binaries into `bin/` and the web bundle                                               |
| `make smoke-up` / `smoke` / `smoke-down` | Build and run the production images locally (HTTPS, mail catcher), run 42 checks, tear down |
| `make vuln`                              | `govulncheck`                                                                               |

## Configuration

All settings come from environment variables; see `.env.example`. Startup fails with a list of every
problem if configuration is invalid. In production (`APP_ENV=production`) a strong unique `SESSION_SECRET`
(32+ chars) and an `https://` `APP_BASE_URL` are required; there is no development fallback.

## Migrations

SQL files live in `migrations/` (`NNNNNN_name.up.sql` / `.down.sql`), embedded into the binaries and applied with
golang-migrate. Never edit an applied migration; add a new one.

## Troubleshooting

- macOS: `go test -race` or cgo fails to link with a newer SDK → `export SDKROOT=/Library/Developer/CommandLineTools/SDKs/MacOSX26.sdk` (or whichever older SDK is installed under that folder).
- `invalid configuration: DATABASE_URL: required` — copy `.env.example` to `.env` (Make loads it) or export the variable.
- `/readyz` returns 503 — PostgreSQL is unreachable; check the API log for the underlying error (it is deliberately not in the response).
- Migration says `dirty` — a migration failed midway; fix the database state manually, then `migrate force <version>` (golang-migrate CLI).
- Port 5432 already in use — stop the local PostgreSQL or change the port mapping in `deploy/docker-compose.yml`.
