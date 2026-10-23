# Backup and restore

All state is in PostgreSQL. Backups are logical dumps (`pg_dump -Fc`, compressed), written mode 600 with a SHA-256 file; restores are verified by a drill.

## Strategy

| | Target | How |
|---|---|---|
| RPO (acceptable data loss) | 24 h with nightly dumps; minutes if your DB provider offers point-in-time recovery (**recommended**: use both) | `scripts/backup.sh` from cron/systemd timer |
| RTO (time to recover) | < 1 h including provisioning | `scripts/restore.sh` + redeploy |
| Retention | 14 daily, 8 weekly offsite | `--keep-days`, plus your storage lifecycle rules |

Monitor credentials (API keys and passwords customers save for API monitors) are in the database but **encrypted**, with the key in `.env.production` as `SECRETS_ENCRYPTION_KEY`. A restore therefore needs *both* the dump and that key: keep the key in a password manager, never in the same place as the dumps, and restore the **same** key. Other secrets are **not** in the database: keep `.env.production` in a password manager/secret store and back it up separately (without `SESSION_SECRET` all users are signed out, without the Stripe/SMTP keys billing and mail stop).

## Taking a backup

```
DATABASE_URL=postgres://... scripts/backup.sh --out /var/backups/hawkeye --keep-days 14
scripts/backup.sh --docker hawkeye-db --user hawkeye --db monitoring        # Postgres in Docker
AGE_RECIPIENT=age1... scripts/backup.sh ...                                  # encrypt to a public key (age)
```

**Copy the file off the host** (object storage with versioning, different account/region). Customer emails and monitor URLs are inside: encrypt (`AGE_RECIPIENT`) before it leaves the machine and keep the private key elsewhere.

### Nightly off-host job (Vultr Object Storage)

`scripts/backup-offsite.sh` takes a dump from the Docker Postgres, encrypts it with `age` (public key in `AGE_RECIPIENT`), uploads it with `rclone` to
`<bucket>/db/`, checks the uploaded size, keeps 30 days there and 3 days locally. Settings come from `~/.hawkeye-host.env`, which `scripts/sync-secrets.sh` writes from the secret manager at every deploy (`docs/secrets.md`); the older `~/.hawkeye-backup.env` (mode 600) still works. They are `AGE_RECIPIENT`,
`BACKUP_ENDPOINT` (e.g. `ewr1.vultrobjects.com`), `BACKUP_BUCKET`, `BACKUP_ACCESS_KEY`, `BACKUP_SECRET_KEY`. Cron runs it nightly and logs to
`~/backups/backup.log`. The age **private** key must live only in a password manager: to restore, download the `.dump.age` and its `.sha256`,
`age -d -i key.txt file.dump.age > file.dump`, then use `scripts/restore.sh` as below. The same job also uploads an encrypted copy of all the server's settings to `<bucket>/secrets/` (how to read it: `docs/secrets.md`, *Recovery*).

## Restoring

```
scripts/restore.sh hawkeye-20260101T020000Z.dump --docker hawkeye-db --user hawkeye --db monitoring_restored --create
DATABASE_URL=postgres://.../monitoring_new scripts/restore.sh DUMP
```

The script verifies the checksum, **refuses to restore into a non-empty database**, and removes a partially restored database on failure. Encrypted dumps must be decrypted first (`age -d -i key.txt file.dump.age > file.dump`).

## Disaster recovery procedure

1. Provision PostgreSQL (and a host if needed), restore the newest good dump into an empty database.
2. Restore `.env.production`, set `DATABASE_URL` to the new database.
3. `migrate` (a no-op if the dump is current, applies newer migrations if the code is newer), `up -d`, run `scripts/smoke.sh`.
4. In Stripe, resend failed webhook events from the outage window, or let the worker reconcile; an owner can re-read a subscription from Stripe with `POST /api/v1/billing/sync`.
5. Checks resume by themselves: the scheduler enqueues overdue monitors. Expect a gap in history for the downtime; it is not backfilled.

## Restore drill

`scripts/verify-restore.sh DUMP --docker CONTAINER --user U --db SOURCE_DB` restores into a temporary database, compares migration version and per-table row counts with the live database, then drops the copy. **Run it monthly and after schema changes.**

Measured on the development machine (PostgreSQL 17 in Docker, 200,000 `check_results` rows, 18 tables): dump 1 s (836 KB), restore 2 s, all 18 table counts identical. Also verified to fail correctly for: tampered dump (checksum mismatch), truncated dump, and restore into a non-empty database. Production data volumes will be larger: re-time the drill on your own data and record the result here.
