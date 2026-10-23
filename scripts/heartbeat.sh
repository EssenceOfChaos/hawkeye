#!/usr/bin/env bash
# Dead-man's switch for healthchecks.io: run every minute from cron on the production server.
#
#   scripts/heartbeat.sh
#
# It asks the site's own public address for /readyz (so DNS, the certificate, Caddy, the API and the database are all in the path). If that
# answers, it pings healthchecks.io; if it does not, it reports a failure straight away. If the whole server is down, nothing runs, no ping
# arrives, and healthchecks.io alerts you when the grace period passes. That second case is the one nothing on the server can report itself.
#
# Setting: HEALTHCHECK_URL=https://hc-ping.com/<your-check-uuid>, a secret of sorts (anyone holding it can send pings), so it is not in git. Set it
# as a secret in the secret manager: scripts/sync-secrets.sh writes it to ~/.hawkeye-host.env at each deploy. The older ~/.hawkeye-heartbeat.env
# (mode 600, one line) still works. Without either, this does nothing.
set -uo pipefail
LEGACY="${HEARTBEAT_ENV:-$HOME/.hawkeye-heartbeat.env}"
HOST_ENV="${HAWKEYE_HOST_ENV:-$HOME/.hawkeye-host.env}"
# shellcheck disable=SC1090
[ -f "$LEGACY" ] && . "$LEGACY"
# shellcheck disable=SC1090
[ -f "$HOST_ENV" ] && . "$HOST_ENV"
[ -n "${HEALTHCHECK_URL:-}" ] || { [ -f "$LEGACY" ] && echo "HEALTHCHECK_URL is not set in $LEGACY" >&2 && exit 1; exit 0; }   # not configured yet: do nothing, quietly
HERE="$(cd "$(dirname "$0")/.." && pwd)"
SITE="$(sed -n 's/^SITE_ADDRESS=\([^ #]*\).*/\1/p' "$HERE/.env.production" 2>/dev/null)"
[ -n "$SITE" ] || { curl -fsS -m 10 --retry 2 -o /dev/null "$HEALTHCHECK_URL/fail" --data-raw "SITE_ADDRESS not found in .env.production" ; exit 1; }

if curl -fsS -m 10 -o /dev/null "https://$SITE/readyz"; then
  curl -fsS -m 10 --retry 3 -o /dev/null "$HEALTHCHECK_URL"
else
  curl -fsS -m 10 --retry 3 -o /dev/null "$HEALTHCHECK_URL/fail" --data-raw "https://$SITE/readyz did not answer"
  exit 1
fi
