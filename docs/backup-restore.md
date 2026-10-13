# Backup and restore

All state is in PostgreSQL. Backups are logical dumps (`pg_dump -Fc`, compressed), written mode 600 with a SHA-256 file; restores are verified by a drill.

## Strategy

| | Target | How |
|---|---|---|
| RPO (acceptable data loss) | 24 h with nightly dumps; minutes if your DB provider offers point-in-time recovery (**recommended**: use both) | `scripts/backup.sh` from cron/systemd timer |
| RTO (time to recover) | < 1 h including provisioning | `scripts/restore.sh` + redeploy |
| Retention | 14 daily, 8 weekly offsite | `--keep-days`, plus your storage lifecycle rules |

Secrets are **not** in the database: keep `.env.production` in a password manager/secret store and back it up separately (without `SESSION_SECRET` all users are signed out, without the Stripe/SMTP keys billing and mail stop).

## Taking a backup

```
DATABASE_URL=postgres://... scripts/backup.sh --out /var/backups/hawkeye --keep-days 14
scripts/backup.sh --docker hawkeye-db --user hawkeye --db monitoring        # Postgres in Docker
AGE_RECIPIENT=age1... scripts/backup.sh ...                                  # encrypt to a public key (age)
```

**Copy the file off the host** (object storage with versioning, different account/region). Customer emails and monitor URLs are inside: encrypt (`AGE_RECIPIENT`) before it leaves the machine and keep the private key elsewhere.

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
