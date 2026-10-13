#!/usr/bin/env bash
# Restore a backup made by scripts/backup.sh into an EMPTY database.
#
#   scripts/restore.sh DUMP --docker CONTAINER --user hawkeye --db TARGET_DB [--create]
#   DATABASE_URL=postgres://.../TARGET_DB scripts/restore.sh DUMP
#
# Safety: it refuses to restore into a database that already contains tables. To replace a database, drop and recreate it yourself,
# deliberately. --create creates the (empty) target database first. Encrypted dumps (.age) must be decrypted before: age -d -i KEY FILE.age > FILE.dump
set -euo pipefail
DUMP="${1:?usage: restore.sh DUMP [--docker C --user U --db DB] [--create]}"; shift
DOCKER=""; PGU=""; PGDB=""; CREATE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --docker) DOCKER="${2:?}"; shift ;; --user) PGU="${2:?}"; shift ;; --db) PGDB="${2:?}"; shift ;; --create) CREATE=1 ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac; shift
done
[ -f "$DUMP" ] || { echo "no such file: $DUMP" >&2; exit 1; }
if [ -f "$DUMP.sha256" ]; then
  ( cd "$(dirname "$DUMP")" && shasum -a 256 -c "$(basename "$DUMP").sha256" >/dev/null ) || { echo "checksum mismatch: the backup is damaged" >&2; exit 1; }
  echo "checksum ok"
fi
case "$DUMP" in *.age) echo "decrypt first (see header)" >&2; exit 1 ;; esac
START=$(date +%s)

if [ -n "$DOCKER" ]; then
  : "${PGU:?}" "${PGDB:?}"
  psql_() { docker exec -i "$DOCKER" psql -U "$PGU" -v ON_ERROR_STOP=1 -qtA "$@"; }
  if [ "$CREATE" = 1 ]; then
    psql_ -d postgres -c "CREATE DATABASE \"$PGDB\""
    # A failed restore must not leave a half-populated database that looks usable.
    trap 'rc=$?; if [ $rc -ne 0 ]; then echo "restore failed: removing the partial database $PGDB" >&2; docker exec -i "$DOCKER" psql -U "$PGU" -qtA -d postgres -c "DROP DATABASE IF EXISTS \"$PGDB\" WITH (FORCE)" >/dev/null 2>&1 || true; fi' EXIT
  fi
  TABLES="$(psql_ -d "$PGDB" -c "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")"
  [ "$TABLES" = 0 ] || { echo "refusing: database $PGDB already has $TABLES tables" >&2; exit 1; }
  docker exec -i "$DOCKER" pg_restore -U "$PGU" -d "$PGDB" --no-owner --no-privileges --exit-on-error < "$DUMP"
else
  : "${DATABASE_URL:?set DATABASE_URL to the EMPTY target database}"
  TABLES="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -qtA -c "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")"
  [ "$TABLES" = 0 ] || { echo "refusing: target already has $TABLES tables" >&2; exit 1; }
  pg_restore --dbname "$DATABASE_URL" --no-owner --no-privileges --exit-on-error "$DUMP"
fi
echo "restore ok ($(( $(date +%s) - START ))s). Next: start the api/worker (they apply any newer migrations) and run scripts/smoke.sh."
