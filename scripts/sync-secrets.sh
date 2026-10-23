#!/usr/bin/env bash
# Refresh this server's settings from the secret manager (Infisical): .env.production for the containers, ~/.hawkeye-host.env for the host scripts.
#
#   scripts/sync-secrets.sh                              # write both files (what scripts/deploy.sh does on every release)
#   scripts/sync-secrets.sh --check                      # fetch and validate only; write nothing
#   scripts/sync-secrets.sh --override NAME=VALUE ...    # extra options go to fetch-secrets.py (a staging copy uses its own address)
#
# Nothing is replaced unless the new settings are complete and acceptable (scripts/fetch-secrets.py validates them against deploy/env.manifest),
# and each file is swapped in atomically, so a failed run leaves the server exactly as it was.
#
# Exit status: 0 done (or no secret manager configured: the existing .env.production stays in use) | 10 the secret manager cannot be reached
# (the existing files are untouched; scripts/deploy.sh carries on with them and says so) | anything else: stop (credentials rejected, values
# unacceptable, bad settings). The reason is printed.
#
# Settings: HAWKEYE_INFISICAL_ENV (default ~/.hawkeye-infisical.env). Outputs: HAWKEYE_APP_ENV_OUT (default .env.production in the repo) and
# HAWKEYE_HOST_ENV_OUT (default ~/.hawkeye-host.env).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$HERE")"
CREDS="${HAWKEYE_INFISICAL_ENV:-$HOME/.hawkeye-infisical.env}"
APP="${HAWKEYE_APP_ENV_OUT:-$ROOT/.env.production}"
HOST="${HAWKEYE_HOST_ENV_OUT:-$HOME/.hawkeye-host.env}"
export HAWKEYE_INFISICAL_ENV="$CREDS"

if [ ! -f "$CREDS" ]; then
  echo "secrets: no secret manager configured on this machine ($CREDS not found): keeping the existing $(basename "$APP")"
  exit 0
fi
if [ "${1:-}" = "--check" ]; then
  exec python3 "$HERE/fetch-secrets.py" check
fi

umask 077
NEW_APP="$APP.new"; NEW_HOST="$HOST.new"
cleanup() { rm -f "$NEW_APP" "$NEW_HOST"; }
trap cleanup EXIT

rc=0
python3 "$HERE/fetch-secrets.py" write --app-out "$NEW_APP" --host-out "$NEW_HOST" "$@" || rc=$?
if [ "$rc" -ne 0 ]; then
  if [ "$rc" -eq 10 ]; then
    echo "secrets: WARNING the secret manager could not be reached; the existing settings stay in place and may be out of date" >&2
  fi
  exit "$rc"
fi

# APP_VERSION is the deploy script's, not the secret manager's: carry the current value over (scripts/deploy.sh sets the new one).
if [ -f "$APP" ] && grep -q '^APP_VERSION=' "$APP"; then grep '^APP_VERSION=' "$APP" >> "$NEW_APP"; fi

[ -f "$APP" ] && cp -p "$APP" "$APP.previous"
mv "$NEW_APP" "$APP"
[ -f "$HOST" ] && cp -p "$HOST" "$HOST.previous"
mv "$NEW_HOST" "$HOST"
echo "secrets: $(basename "$APP") and $(basename "$HOST") refreshed from the secret manager"
