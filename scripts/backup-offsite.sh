#!/usr/bin/env bash
# Nightly off-host backup: pg_dump (scripts/backup.sh) -> encrypt with age -> upload to S3-compatible object storage (Vultr) -> prune.
#
#   scripts/backup-offsite.sh            # run from cron; settings come from ~/.hawkeye-backup.env (mode 600) or the environment
#
# Settings (all required unless noted):
#   AGE_RECIPIENT      age PUBLIC key (age1...). Dumps hold customer emails and monitor URLs, so they are encrypted before they leave the
#                      host. Keep the matching private key in a password manager, never on this server or in the bucket.
#   BACKUP_ENDPOINT    object storage host, e.g. ewr1.vultrobjects.com
#   BACKUP_BUCKET      bucket name (private)
#   BACKUP_ACCESS_KEY / BACKUP_SECRET_KEY
#   DB_CONTAINER       default hawkeye-db-1      DB_USER default hawkeye      DB_NAME default monitoring
#   BACKUP_DIR         local staging dir, default ~/backups (last 3 days kept)
#   RETENTION_DAYS     how long dumps stay in the bucket, default 30
# Needs: docker, age, rclone (sudo apt install age rclone).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${BACKUP_ENV:-$HOME/.hawkeye-backup.env}"
if [ -f "$ENV_FILE" ]; then set -a; . "$ENV_FILE"; set +a; fi
: "${AGE_RECIPIENT:?set AGE_RECIPIENT}" "${BACKUP_ENDPOINT:?set BACKUP_ENDPOINT}" "${BACKUP_BUCKET:?set BACKUP_BUCKET}"
: "${BACKUP_ACCESS_KEY:?set BACKUP_ACCESS_KEY}" "${BACKUP_SECRET_KEY:?set BACKUP_SECRET_KEY}"
OUT="${BACKUP_DIR:-$HOME/backups}"; RETENTION="${RETENTION_DAYS:-30}"
for bin in docker age rclone; do command -v "$bin" >/dev/null || { echo "$bin is not installed" >&2; exit 1; }; done

# rclone configured entirely from the environment: no credentials file on disk beyond the env file.
export RCLONE_CONFIG_OFFSITE_TYPE=s3 RCLONE_CONFIG_OFFSITE_PROVIDER=Other
export RCLONE_CONFIG_OFFSITE_ENDPOINT="https://$BACKUP_ENDPOINT"
export RCLONE_CONFIG_OFFSITE_ACCESS_KEY_ID="$BACKUP_ACCESS_KEY" RCLONE_CONFIG_OFFSITE_SECRET_ACCESS_KEY="$BACKUP_SECRET_KEY"
DEST="offsite:$BACKUP_BUCKET/db"

AGE_RECIPIENT="$AGE_RECIPIENT" "$HERE/backup.sh" --docker "${DB_CONTAINER:-hawkeye-db-1}" --user "${DB_USER:-hawkeye}" --db "${DB_NAME:-monitoring}" \
  --out "$OUT" --keep-days 3
FILE="$(ls -t "$OUT"/hawkeye-*.dump.age | head -1)"
rclone copy "$FILE" "$DEST/" --checksum
rclone copy "$FILE.sha256" "$DEST/" --checksum
# Prove it arrived with the right size before reporting success.
REMOTE_SIZE="$(rclone size "$DEST/$(basename "$FILE")" --json | sed -n 's/.*"bytes":\([0-9]*\).*/\1/p')"
[ "$REMOTE_SIZE" = "$(wc -c < "$FILE" | tr -d ' ')" ] || { echo "upload size mismatch (remote $REMOTE_SIZE)" >&2; exit 1; }
rclone delete "$DEST/" --min-age "${RETENTION}d" --include 'hawkeye-*'
echo "offsite backup ok: $(basename "$FILE") -> $BACKUP_BUCKET/db/ (${REMOTE_SIZE} bytes), kept ${RETENTION} days"
