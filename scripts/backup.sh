#!/usr/bin/env bash
# Logical backup of the HawkEye database (pg_dump, custom format: compressed, restorable selectively and in parallel).
#
#   DATABASE_URL=postgres://... scripts/backup.sh [--out DIR] [--keep-days N]
#   scripts/backup.sh --docker CONTAINER --user hawkeye --db monitoring [--out DIR]     # when PostgreSQL runs in Docker
#
# Writes DIR/hawkeye-YYYYmmddTHHMMSSZ.dump (+ .sha256), mode 600. If AGE_RECIPIENT is set and `age` is installed the file is encrypted to
# that public key (hawkeye-....dump.age) and the plaintext is removed. COPY THE RESULT OFF THIS HOST: a backup on the machine it protects is not a backup.
# The pg_dump client must be the same major version as the server (or newer); --docker uses the server's own.
set -euo pipefail
OUT="./backups"; KEEP=""; DOCKER=""; PGU=""; PGDB=""
while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT="${2:?}"; shift ;;
    --keep-days) KEEP="${2:?}"; shift ;;
    --docker) DOCKER="${2:?}"; shift ;;
    --user) PGU="${2:?}"; shift ;;
    --db) PGDB="${2:?}"; shift ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac; shift
done
umask 077
mkdir -p "$OUT"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
FILE="$OUT/hawkeye-$STAMP.dump"
START=$(date +%s)

if [ -n "$DOCKER" ]; then
  : "${PGU:?--user is required with --docker}" "${PGDB:?--db is required with --docker}"
  docker exec -i "$DOCKER" pg_dump -U "$PGU" -d "$PGDB" --format=custom --no-owner --no-privileges > "$FILE"
else
  : "${DATABASE_URL:?set DATABASE_URL (or use --docker)}"
  pg_dump --dbname "$DATABASE_URL" --format=custom --no-owner --no-privileges --file "$FILE"
fi

# A dump that cannot be listed is not a backup: fail loudly now rather than at restore time.
if [ -n "$DOCKER" ]; then
  docker exec -i "$DOCKER" pg_restore --list < "$FILE" > /dev/null
else
  pg_restore --list "$FILE" > /dev/null
fi
SIZE=$(wc -c < "$FILE" | tr -d ' ')
[ "$SIZE" -gt 1000 ] || { echo "backup is suspiciously small ($SIZE bytes)" >&2; rm -f "$FILE"; exit 1; }

if [ -n "${AGE_RECIPIENT:-}" ]; then
  command -v age >/dev/null || { echo "AGE_RECIPIENT is set but 'age' is not installed" >&2; exit 1; }
  age -r "$AGE_RECIPIENT" -o "$FILE.age" "$FILE" && rm -f "$FILE"
  FILE="$FILE.age"
fi
( cd "$OUT" && shasum -a 256 "$(basename "$FILE")" > "$(basename "$FILE").sha256" )

if [ -n "$KEEP" ]; then
  find "$OUT" -name 'hawkeye-*.dump*' -type f -mtime +"$KEEP" -delete
fi
echo "backup ok: $FILE ($(wc -c < "$FILE" | tr -d ' ') bytes, $(( $(date +%s) - START ))s)"
