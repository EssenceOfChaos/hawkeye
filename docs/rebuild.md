# Rebuilding the server (infrastructure as code)

`infra/` describes the production server in OpenTofu/Terraform: a Vultr instance (Ubuntu 24.04 LTS, 2 vCPU / 4 GB, region `ewr`), a network firewall (22, 80, 443), your SSH key, a
**reserved IP** that stays yours when the server is replaced, and the **Cloudflare DNS** records for the site (apex A, `www`, and a test name). First-boot setup
(`infra/cloud-init.yaml.tftpl`) creates the `deploy` user, key-only SSH, `ufw`, `fail2ban`, Docker, a swap file, clones this repo to `/home/deploy/hawkeye`, installs the worker
egress firewall as a systemd unit, and installs the nightly backup and the heartbeat crons.

`scripts/rebuild.sh` drives the whole thing from your laptop, step by step, checking each step before the next. Releases are still done by `scripts/deploy.sh` / the Deploy workflow.

## What you keep on your laptop (not in git)

The settings themselves (the live Stripe keys, `SECRETS_ENCRYPTION_KEY`, the backup bucket keys, the heartbeat URL) live in the secret manager, Infisical, and are not
files on your laptop: see `docs/secrets.md`. What you keep:

| What | Where | Why |
|---|---|---|
| `.hawkeye-infisical.env` | `~/.hawkeye-secrets/` | the credentials of a read-only machine identity. `create` copies it to each new server, which then fetches every setting itself. Keep a copy in the password manager. |
| `hawkeye-backup-key.txt` | `~/` (or set `AGE_KEY_FILE`) | the age **private** key: the only way to read a backup |
| `hawkeye-deploy-key.pub` | `~/` | the GitHub Actions deploy key's public half |
| tokens | shell environment | `VULTR_API_KEY`, `CF_DNS_API_TOKEN` (Zone > DNS > Edit, this zone only), `CF_ZONE_ID`, `CF_ACC_ID` |

Keep copies of the first three in a password manager: they are the recovery kit if the laptop dies. **Before the secret manager is set up** (or if you prefer not to use it),
`rebuild.sh` still works from the older files: `.env.production`, `.hawkeye-backup.env` and (optional) `.hawkeye-heartbeat.env` in `~/.hawkeye-secrets/`, which it copies onto
new servers. When `.hawkeye-infisical.env` exists it is used instead and `preflight` says so. Terraform **state** (`infra/terraform.tfstate`) holds the server's generated root
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

```
scripts/rebuild.sh credentials --host <ip>   # an EXISTING server (not one made by `create`): give it the credentials file once, and check it can fetch
```

**What `cutover` does** (and why it asks first): it stops the old server's web, api and worker (the outage starts), takes a final backup there, restores it onto the new server, moves
the apex A record (TTL 60 s) to the new reserved IP, starts the full stack (Caddy gets its certificate on the first request), smoke-tests `https://<domain>`, and updates the
GitHub secrets `DEPLOY_HOST` / `DEPLOY_KNOWN_HOSTS`. Expect roughly 5 to 15 minutes of downtime; checks pause and catch up afterwards. The old server is left **stopped, not deleted**.
Customers' alerts and sessions survive: the database moves with the backup, and the new server fetches the same settings (including the session secret) from the secret manager.

**How a new server gets its settings:** `create` copies `.hawkeye-infisical.env` to it. `stage` runs `scripts/sync-secrets.sh` on the server with the stage's own address in place of
the real one; `cutover` runs it again for the real values. Nothing secret is typed or copied by hand, and nothing secret is in the infrastructure code or in cloud-init.

**Why a staged copy never sends alerts:** a stage or rehearsal runs `db`, `migrate`, `api` and `web` only. The worker (scheduler, checks, alert delivery) starts only at the cutover,
so a copy of production cannot run checks or email your customers.

## Heartbeat (healthchecks.io)

`scripts/heartbeat.sh` runs every minute from cron (installed by first boot; silent until a `HEALTHCHECK_URL` is set). It requests the site's own public `/readyz` and pings
healthchecks.io on success or `/fail` on failure. If the whole server dies, the pings stop and healthchecks.io alerts you after the grace period. In healthchecks.io: period 1 minute,
grace 3 to 5 minutes, and an email (or Telegram/Slack) integration. Add the check's ping URL as `HEALTHCHECK_URL` in the secret manager (`docs/secrets.md`); it reaches the server's
`~/.hawkeye-host.env` at the next deploy or settings refresh, and new servers get it automatically. (The older `~/.hawkeye-heartbeat.env` still works.)
To switch it on for a server made before the cron entry existed: add `* * * * * deploy /home/deploy/hawkeye/scripts/heartbeat.sh >> /home/deploy/backups/heartbeat.log 2>&1` to `/etc/cron.d/hawkeye-heartbeat`.

## Day to day

- The server is **replaceable, not editable**. `ignore_changes = [user_data]` means editing the cloud-init file does not touch a running server; to apply it, replace the server on
  purpose (`tofu apply -replace=vultr_instance.server`) and repeat `stage`/`cutover`-style steps. The reserved IP carries over.
- Changing `hostname`, the OS or the region also replaces the server. Always read `tofu plan` first and look for `must be replaced` and `destroy`.
- DNS: only the apex A, `www` and the stage name are managed. Mail, Resend and Email Routing records are never touched. `live.auto.tfvars` (git-ignored) records `cutover` and the old IP; do not
  edit it by hand.
- Cost: the instance bills hourly until destroyed; a reserved IP and Vultr's automatic backups add to it (`enable_backups = false` turns the backups off). A rehearsal costs cents if you destroy it.
- First boot is proven only by running it. The egress rules still need `scripts/verify-egress.sh` on the new server.
