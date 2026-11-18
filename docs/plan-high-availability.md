# Plan: high availability (surviving a server failure)

Status: **planned, not started** (2026-11-17). Today HawkEye is one server with everything on it; this is the path to surviving the loss of that
server without an outage, in steps that each stand on their own. Every infrastructure change here is a reviewed pull request applied by CI
(`docs/deployment.md`, "Infrastructure changes").

## Where we are

Production is **one Vultr server** in New Jersey (`ewr`) running Caddy, the API, the web app, the worker, PostgreSQL, Prometheus and Alertmanager.
The inventory taken while adopting it into OpenTofu (2026-11-17) found it differs from what the docs assumed:

| | The docs assumed | Actually |
|---|---|---|
| Size | `vc2-2c-4gb` (2 vCPU / 4 GB) | `vc2-1c-2gb` (1 vCPU / 2 GB); resized to `vc2-2c-4gb` 2026-11-18 (step 1.2) |
| OS | Ubuntu 24.04 LTS | Ubuntu 26.10 (not LTS: about 9 months of updates) |
| Address | a reserved IP | the server's own address (45.77.216.247) |
| Network firewall | a Vultr firewall group | none (ufw on the server only); added 2026-11-17 (step 1.1) |

Backups: a nightly encrypted database dump to Vultr Object Storage, and Vultr's whole-server backups. Recovery today is a rebuild from those
(`docs/rebuild.md`), documented at under an hour. A failed server means **an outage of up to an hour, and up to a day of lost writes** if the
nightly dump is the newest copy.

**The size matters before anything else.** The load test behind 30-second checks (`docs/plan-sub-minute-checks.md`) gave PostgreSQL 2 CPUs and
2 GB on its own; production had 1 CPU and 2 GB for everything, hence the resize (step 1.2). Rerun the load test against the new shape.

## What a load balancer does and doesn't do

A Vultr load balancer (from $10 a month) health-checks its servers and stops sending traffic to one that fails. It only helps when there is
another working server behind it **and** a database that outlives either server. It lives in one Vultr location, so it doesn't survive that
location going down. It doesn't terminate TLS for us: it must pass TCP through to Caddy, which gets certificates for HawkEye and for customers'
status page domains (`on_demand_tls`).

Horizontal scaling isn't the reason to do this: the load test put 1,400 monitors at 30-second checks within one server's reach (once resized).
Availability is.

## Goal

Survive the loss of any one server, and deploy without downtime: about 99.95% a month, excluding a whole-location outage. "Never any downtime" isn't
a goal any single-location design meets; surviving a location needs a second region and DNS failover (step 5).

## Steps

### 1. Bring production in line (pull requests to `infra/`, no new services)

Each is a small reviewed change applied by CI; the guard refuses anything that would replace the server.

1. **Done: Vultr firewall group** on the server (SSH, HTTP, HTTPS), as for any server `infra/` creates. Before attaching it, only TCP 22, 80 and 443
   answered from outside (2026-11-17), so it closes nothing that was in use.
   In place, no restart. Also a prerequisite for regional workers (their tunnel rule lives in that group).
2. **Done: resize to `vc2-2c-4gb`** (`plan` in `infra/production.auto.tfvars`), 2026-11-18. In place; Vultr restarts the server, so a few minutes
   of downtime, applied when the pull request was merged. The disk grows with the plan (55 to 80 GB) and Ubuntu grows the root filesystem at boot.
3. **A reserved IP**: convert the server's current address into a reserved IP so DNS and `DEPLOY_HOST` never change when the server is replaced.
   Check that the provider can do this without a new address; if not, convert it with Vultr's API and import the result.
4. **An LTS OS**: moving from 26.10 to 24.04 LTS (or the next LTS) means a new server: a deliberate rebuild (`docs/rebuild.md`, run from the
   Infrastructure workflow with `vultr_instance.server` allowed to be replaced), after which the server is no longer "adopted". Before 26.10's
   updates end.

### 2. Test that recovery works (free)

- A scheduled restore drill: monthly, CI restores the newest off-site dump into a throwaway database and runs the checks in
  `docs/backup-restore.md`. Today the drill is manual; a recovery that has never been tested isn't one.
- Alert when the newest off-site dump is older than 26 hours.

### 3. Managed PostgreSQL with a standby

The step that removes the most likely long outage, and frees the app server's memory and CPU.

- A Vultr Managed Database (PostgreSQL 17, matching production) with **a standby replica** that takes over automatically, in the same location,
  on a private network (VPC) with the app servers. Point-in-time recovery shortens lost writes from a day to minutes. Managed in `infra/`
  (`vultr_database`), its credentials going to the secret manager.
- **Move**: a short maintenance window: stop the app, dump, restore into the managed database, switch `DATABASE_URL`, start, verify. Rehearse it on a
  drill workspace first. Keep the old database read-only for a week.
- **Checks**: connection limits (the worker's pool is 34 connections at `WORKER_CONCURRENCY=30`; the API adds its own), and that a standby
  takeover (a few seconds to a minute) only makes checks wait, never loses results: the queue is durable and the scheduler catches up.
- **Regional workers** reach PostgreSQL through a WireGuard tunnel to the production server today. With a managed database they need another route
  (the database's trusted sources over its public endpoint, or the VPC through a tunnel host); decide before launching a region.
- Cost: check Vultr's current price for a primary plus one standby of the size the load test needs.

### 4. Two app servers behind a load balancer

- **Servers**: two identical servers from `infra/` (one module, `count = 2`), each running Caddy, the API, the web app and a worker; Prometheus and
  Alertmanager on one of them (or moved to a small server of their own). Both get the egress firewall.
- **Load balancer** (`vultr_load_balancer` in `infra/`): TCP pass-through on 80 and 443, health check on `/readyz`, and **PROXY protocol** on, with
  Caddy's `proxy_protocol` listener wrapper, so the API still sees visitors' real addresses (per-IP rate limits, the security log and sign-in
  throttling depend on them). DNS points at the load balancer's address.
- **Certificates**: both Caddys must share certificate storage, or each asks Let's Encrypt for the same certificates and an ACME challenge can land
  on the server that didn't ask. Use a shared Caddy storage backend (S3-compatible object storage or PostgreSQL), which means building Caddy with that
  module (`xcaddy`) in `deploy/Dockerfile.web`. This covers customers' status page domains too.
- **Workers and the scheduler**: workers already share the queue safely (`FOR UPDATE SKIP LOCKED`). Confirm the scheduler, rollups, retention and
  the other single-instance jobs take their advisory locks before running two (`cmd/worker`), or run them on one server only.
- **Deploys without downtime**: `deploy.yml` deploys one server at a time; the load balancer's health check takes each out while it restarts.
  Migrations must keep working with the previous release, which is already the rule (expand/contract, checked in CI).
- Cost: the load balancer (from $10 a month) and the second server.

### 5. Another location (only if customers need it)

A second location with its own servers and a database replica, and DNS failover between them (health-checked DNS, short TTLs). Promoting a
replica in another location is the hard part (lost writes, split brain), so this is a separate plan when contracts ask for it.

## Order and when

1. Steps 1.1 and 1.2 are done. Next: regional check workers (`docs/deployment.md`, "Regional worker hosts"), which paid plans already offer.
2. Step 2 now: free, and it is what makes the current one-hour recovery real.
3. Steps 1.3, 1.4 before 26.10's updates end.
4. Steps 3 and 4 before HawkEye has many paying customers: a monitoring service that is down can't tell its customers their sites are down. Step 3
   first: it is the larger risk, and step 4 needs it.
