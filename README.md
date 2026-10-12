# Hawkeye

Multi-tenant website monitoring SaaS: add websites, detect downtime, get alerts, review uptime.
Go + Echo REST API, PostgreSQL, React + TypeScript frontend.

**Status:** Phases 1–5 complete: foundation, authentication & tenancy, monitor management with plan limits, the monitoring engine
(durable scheduler/queue, SSRF-safe checks), and incidents with confirmed-outage/recovery email alerts. Email is still the `console` provider
(messages are logged, not delivered). Dashboard/charts/uptime arrive in Phase 6, billing in Phase 7.
See `docs/architecture.md` for the design.

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
API reference: `docs/api.md`. Security notes: `docs/security.md`. Running the engine: `docs/operations.md`. Brand assets: `docs/branding.md`.

## Commands

| Command | Purpose |
|---|---|
| `make migrate` / `migrate-down` | Apply / revert one migration (`cmd/migrate`) |
| `make run-api` | Run the API |
| `make run-worker` | Run the monitoring engine (scheduler + workers + `/metrics`) |
| `make test`, `make test-race` | Go tests |
| `make lint` | gofmt, vet, web lint |
| `make build` | Build binaries into `bin/` and the web bundle |

## Configuration

All settings come from environment variables; see `.env.example`. Startup fails with a list of every
problem if configuration is invalid. In production (`APP_ENV=production`) a strong unique `SESSION_SECRET`
(32+ chars) and an `https://` `APP_BASE_URL` are required; there is no development fallback.

## Migrations

SQL files live in `migrations/` (`NNNNNN_name.up.sql` / `.down.sql`), embedded into the binaries and applied with
golang-migrate. Never edit an applied migration; add a new one.

## Troubleshooting

- `invalid configuration: DATABASE_URL: required` — copy `.env.example` to `.env` (Make loads it) or export the variable.
- `/readyz` returns 503 — PostgreSQL is unreachable; check the API log for the underlying error (it is deliberately not in the response).
- Migration says `dirty` — a migration failed midway; fix the database state manually, then `migrate force <version>` (golang-migrate CLI).
- Port 5432 already in use — stop the local PostgreSQL or change the port mapping in `deploy/docker-compose.yml`.
