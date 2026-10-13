#!/usr/bin/env bash
# Restore drill: restores a backup into a temporary database, compares it with the live source database, then drops it.
# Run this regularly (e.g. monthly, and after every schema change) so you KNOW a backup works before you need it.
#
#   scripts/verify-restore.sh DUMP --docker CONTAINER --user hawkeye --db monitoring
#
# Compares: schema migration version, and the row count of every table. Rows written after the backup was taken will make live counts
# larger, which is reported as a note, not a failure; a restored count LARGER than live, or a missing table, fails.
set -euo pipefail
DUMP="${1:?usage: verify-restore.sh DUMP --docker C --user U --db SOURCE_DB}"; shift
DOCKER=""; PGU=""; PGDB=""
while [ $# -gt 0 ]; do
  case "$1" in --docker) DOCKER="${2:?}"; shift ;; --user) PGU="${2:?}"; shift ;; --db) PGDB="${2:?}"; shift ;; *) echo "unknown option $1" >&2; exit 2 ;; esac; shift
done
: "${DOCKER:?only --docker mode is implemented for the drill}" "${PGU:?}" "${PGDB:?}"
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="restore_verify_$(date -u +%Y%m%d%H%M%S)"
psql_() { docker exec -i "$DOCKER" psql -U "$PGU" -v ON_ERROR_STOP=1 -qtA "$@"; }
cleanup() { psql_ -d postgres -c "DROP DATABASE IF EXISTS \"$TMP\" WITH (FORCE)" >/dev/null 2>&1 || true; }
trap cleanup EXIT

"$HERE/restore.sh" "$DUMP" --docker "$DOCKER" --user "$PGU" --db "$TMP" --create

counts() { # db -> "table count" lines
  local db="$1" t
  for t in $(psql_ -d "$db" -c "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY 1"); do
    echo "$t $(psql_ -d "$db" -c "SELECT count(*) FROM \"$t\"")"
  done
}
LIVE="$(counts "$PGDB")"; REST="$(counts "$TMP")"
VL="$(psql_ -d "$PGDB" -c "SELECT version||'/'||dirty FROM schema_migrations")"; VR="$(psql_ -d "$TMP" -c "SELECT version||'/'||dirty FROM schema_migrations")"
echo "schema version live=$VL restored=$VR"
FAIL=0
while read -r table live; do
  rest="$(echo "$REST" | awk -v t="$table" '$1==t{print $2}')"
  if [ -z "$rest" ]; then echo "  MISSING table in restore: $table"; FAIL=1
  elif [ "$rest" -gt "$live" ]; then echo "  MISMATCH $table: restored $rest > live $live"; FAIL=1
  elif [ "$rest" -lt "$live" ]; then echo "  note $table: restored $rest, live $live (rows written after the backup)"
  else printf '  ok   %-30s %s rows\n' "$table" "$rest"; fi
done <<< "$LIVE"
[ "$VL" = "$VR" ] || echo "  note: schema version differs (a migration ran after the backup)"
if [ "$FAIL" = 0 ]; then echo "RESTORE DRILL PASSED"; else echo "RESTORE DRILL FAILED"; exit 1; fi
