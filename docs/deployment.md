# Deployment

A single-host production deployment with Docker Compose. Everything here was exercised locally with `make smoke-up` (the same images and Compose file, with a private CA and a mail catcher instead of real ACME and SMTP).

## Layout

```
internet ─443/80─▶ web (Caddy: TLS, HSTS, CSP, static SPA, proxies /api /healthz /readyz)
                      │  network "edge"/"backend"
                      ▼
                    api  (:8080, metrics :9091 private) ─▶ PostgreSQL
                    worker (scheduler, check workers, dispatcher, retention, billing processor; :9090 private)
                      │  network "checks" 172.30.10.0/24 (the only network with internet egress for customer URLs)
                      ▼
                  customers' websites
```

* Only `web` publishes ports. The metrics ports and PostgreSQL are never published.
* Containers are read-only, run non-root with all capabilities dropped and `no-new-privileges`. API and worker are distroless.
* PostgreSQL is **external by default** (managed database recommended). `docker-compose.local-db.yml` adds a single-node Postgres for small installs and the smoke stack.

## Prerequisites

* A Linux host with Docker Engine and Compose v2 (2.20+), ports 80 and 443 reachable, a DNS A/AAAA record for your domain.
* PostgreSQL 15+ with TLS (`sslmode=require` or stricter; production refuses plaintext unless `ALLOW_INSECURE_DATABASE=true`).
* An SMTP relay (SES, Postmark, Mailgun, ...) with SPF/DKIM/DMARC for the sender domain. Production refuses the console email provider and unencrypted SMTP.
* Stripe account with Pro/Agency prices (see `docs/billing.md`) if you charge for plans.

## First deployment

1. `cp deploy/.env.production.example .env.production` and fill every `CHANGE_ME`. Generate `SESSION_SECRET` with `openssl rand -base64 48`. Keep the file `chmod 600`, out of git.
2. Run migrations once: `docker compose --env-file .env.production -f deploy/docker-compose.prod.yml run --rm migrate` (or `make migrate` against the target `DATABASE_URL`). Migrations are embedded in the binary and forward-only.
3. Build and start: `docker compose --env-file .env.production -f deploy/docker-compose.prod.yml up -d --build`. Caddy obtains the certificate on first request; watch `docker compose logs -f web`.
4. Verify: `scripts/smoke.sh https://your-domain` (30 checks: health, redirect to HTTPS, HSTS and CSP headers, security-sensitive routes, auth flow; `--full` also drives the registration and account-deletion lifecycle and needs a mail API).
5. Apply the egress firewall on the host and prove it: `sudo ALLOW_TCP=... ALLOW_DNS=... deploy/egress-filter.sh apply`, then `scripts/verify-egress.sh`. **This script has not been tested on Linux by the author; do not skip the verifier.**
6. Add the Stripe webhook endpoint `https://your-domain/api/v1/webhooks/stripe`, put its signing secret in `STRIPE_WEBHOOK_SECRET`, restart `api` and `worker`.
7. Load `deploy/prometheus-alerts.yml`, add an external uptime check on `/readyz`, schedule backups (`docs/backup-restore.md`).

## Configuration

`deploy/.env.production.example` lists every variable with comments; `internal/config/config.go` is the authoritative parser and validator. In production (`APP_ENV=production`) the process **refuses to start** if: `SESSION_SECRET` is short or a placeholder, base URLs are not https, `EMAIL_PROVIDER` is not smtp, `SMTP_TLS=none`, the sender is an example domain, or the database URL does not use TLS.

## Upgrades

1. Take a backup (`scripts/backup.sh`), and note the current `APP_VERSION`.
2. Build the new images (`docker compose ... build`), run `migrate`, then `up -d` (Compose replaces containers; the worker's leases make in-flight checks safe to interrupt, they are re-run).
3. Re-run `scripts/smoke.sh`. Watch error rate and `hawkeye_http_panics_total` for 15 minutes.

Migrations are **forward-only**. Make every schema change *expand/contract*: release N adds columns/tables that N-1 code ignores; release N+1 removes what nothing reads. This keeps rollback of the application safe.

## Rollback

* **Application only** (no migration in the release, or the migration was additive): re-deploy the previous image tag/commit and `up -d`. Old code runs fine against an expanded schema.
* **Bad migration/data damage**: stop `api` and `worker`, restore the pre-upgrade backup into a fresh database (`docs/backup-restore.md`), point `DATABASE_URL` at it, deploy the previous version. You lose writes since the backup; take the backup immediately before upgrading to minimise that.

## Scaling and availability

`api` is stateless (sessions, rate limits and the job queue live in PostgreSQL), so run several replicas behind Caddy. Workers scale horizontally (jobs are claimed with `FOR UPDATE SKIP LOCKED`; `DISABLE_SCHEDULER=true` on all but one or two is fine, duplicates are harmless). A single host is a single point of failure: for HA, put the database on a managed HA service and run the stack on two hosts behind a load balancer.

## Local production-like stack

`make smoke-up` builds the production images and starts them with a local Postgres, a private-CA TLS mail catcher and a self-signed site on `https://localhost:8443`; `make smoke` runs the checks; `make smoke-down` removes it. It uses its own Compose project (`hawkeye-smoke`) and never touches the dev stack.

## Troubleshooting

* Container exits at start with a config error → the message names the variable.
* Certificate not issued → DNS must point at the host and 80/443 must be open; `docker compose logs web`.
* `/readyz` 503 → database unreachable or migrations missing.
* Behind another proxy/load balancer, terminate TLS there only if it forwards the real client IP correctly, otherwise per-IP rate limits will see one IP.
