# Rebuilding the server (infrastructure as code)

`infra/` describes the production server in OpenTofu/Terraform: a Vultr instance (Ubuntu 24.04 LTS, 2 vCPU / 4 GB, region `ewr`), a network firewall (22, 80, 443), your SSH key, a
**reserved IP** that stays yours when the server is replaced, and the **Cloudflare DNS** records for the site (apex A, `www`, and a test name). First-boot setup
(`infra/cloud-init.yaml.tftpl`) creates the `deploy` user, key-only SSH, `ufw`, `fail2ban`, Docker, a swap file, clones this repo to `/home/deploy/hawkeye`, installs the worker
egress firewall as a systemd unit, and installs the nightly backup and the heartbeat crons.

`scripts/rebuild.sh` drives the whole thing from your laptop, step by step, checking each step before the next. Releases are still done by `scripts/deploy.sh` / the Deploy workflow.

## What you keep on your laptop (not in git)

| What | Where | Why |
|---|---|---|
| `.env.production` | `~/.hawkeye-secrets/` | copied onto new servers (it holds the live Stripe keys and `SECRETS_ENCRYPTION_KEY`) |
| `.hawkeye-backup.env` | `~/.hawkeye-secrets/` | bucket keys and the age public key for the nightly dumps |
| `.hawkeye-heartbeat.env` (optional) | `~/.hawkeye-secrets/` | one line, `HEALTHCHECK_URL=https://hc-ping.com/<uuid>` from healthchecks.io |
| `hawkeye-backup-key.txt` | `~/` (or set `AGE_KEY_FILE`) | the age **private** key: the only way to read a backup |
| `hawkeye-deploy-key.pub` | `~/` | the GitHub Actions deploy key's public half |
| tokens | shell environment | `VULTR_API_KEY`, `CF_DNS_API_TOKEN` (Zone > DNS > Edit, this zone only), `CF_ZONE_ID`, `CF_ACC_ID` |

Keep copies of the first four in a password manager: they are the recovery kit if the laptop dies. Terraform **state** (`infra/terraform.tfstate`) holds the server's generated root
password; it is git-ignored, keep it private and backed up.

## The steps

```
scripts/rebuild.sh preflight                 # read-only: tools, tokens, secrets, pushed code, backups in the bucket
scripts/rebuild.sh create --workspace drill  # REHEARSAL: a throwaway server and its own test name (drill.<domain>); never touches the real site
scripts/rebuild.sh stage  --workspace drill  # restore the newest backup, start it WITHOUT the worker, smoke-test it
scripts/rebuild.sh destroy --workspace drill # delete the rehearsal
```
Then the same for real: `create`, `stage`, look around `https://staging.<domain>` (log in with your real account), and when you are happy:

```
scripts/rebuild.sh cutover                   # you must type: cutover <domain>
scripts/rebuild.sh rollback                  # if it goes wrong: DNS back to the old server, old stack restarted
```
Add `--dry-run` to any command to print what it would do. `preflight` and `plan` never change anything.

**What `cutover` does** (and why it asks first): it stops the old server's web, api and worker (the outage starts), takes a final backup there, restores it onto the new server, moves
the apex A record (TTL 60 s) to the new reserved IP, starts the full stack (Caddy gets its certificate on the first request), smoke-tests `https://<domain>`, and updates the
GitHub secrets `DEPLOY_HOST` / `DEPLOY_KNOWN_HOSTS`. Expect roughly 5 to 15 minutes of downtime; checks pause and catch up afterwards. The old server is left **stopped, not deleted**.
Customers' alerts and sessions survive: the database and the session secret move with the backup and `.env.production`.

**Why a staged copy never sends alerts:** a stage or rehearsal runs `db`, `migrate`, `api` and `web` only. The worker (scheduler, checks, alert delivery) starts only at the cutover,
so a copy of production cannot run checks or email your customers.

## Heartbeat (healthchecks.io)

`scripts/heartbeat.sh` runs every minute from cron (installed by first boot; silent until `~/.hawkeye-heartbeat.env` exists). It requests the site's own public `/readyz` and pings
healthchecks.io on success or `/fail` on failure. If the whole server dies, the pings stop and healthchecks.io alerts you after the grace period. In healthchecks.io: period 1 minute,
grace 3 to 5 minutes, and an email (or Telegram/Slack) integration. Put the check's ping URL in `~/.hawkeye-secrets/.hawkeye-heartbeat.env`; the cutover copies it to the new server.
To switch it on for the current server before the cutover: copy the file to the server and add `* * * * * deploy /home/deploy/hawkeye/scripts/heartbeat.sh` to `/etc/cron.d/hawkeye-heartbeat`.

## Day to day

- The server is **replaceable, not editable**. `ignore_changes = [user_data]` means editing the cloud-init file does not touch a running server; to apply it, replace the server on
  purpose (`tofu apply -replace=vultr_instance.server`) and repeat `stage`/`cutover`-style steps. The reserved IP carries over.
- Changing `hostname`, the OS or the region also replaces the server. Always read `tofu plan` first and look for `must be replaced` and `destroy`.
- DNS: only the apex A, `www` and the stage name are managed. Mail, Resend and Email Routing records are never touched. `live.auto.tfvars` (git-ignored) records `cutover` and the old IP; do not
  edit it by hand.
- Cost: the instance bills hourly until destroyed; a reserved IP and Vultr's automatic backups add to it (`enable_backups = false` turns the backups off). A rehearsal costs cents if you destroy it.
- First boot is proven only by running it. The egress rules still need `scripts/verify-egress.sh` on the new server.
