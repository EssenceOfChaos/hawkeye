#!/usr/bin/env bash
# Dead-man's switch for healthchecks.io: run every minute from cron on the production server.
#
#   scripts/heartbeat.sh
#
# It asks the site's own public address for /readyz (so DNS, the certificate, Caddy, the API and the database are all in the path). If that
# answers, it pings healthchecks.io; if it does not, it reports a failure straight away. If the whole server is down, nothing runs, no ping
# arrives, and healthchecks.io alerts you when the grace period passes. That second case is the one nothing on the server can report itself.
#
# Settings, in ~/.hawkeye-heartbeat.env (mode 600, one line):  HEALTHCHECK_URL=https://hc-ping.com/<your-check-uuid>
# The URL is a secret of sorts (anyone holding it can send pings), so it lives there and not in git.
set -uo pipefail
ENV_FILE="${HEARTBEAT_ENV:-$HOME/.hawkeye-heartbeat.env}"
[ -f "$ENV_FILE" ] || exit 0   # not configured yet: do nothing, quietly
# shellcheck disable=SC1090
. "$ENV_FILE"
: "${HEALTHCHECK_URL:?set HEALTHCHECK_URL in $ENV_FILE}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"
SITE="$(sed -n 's/^SITE_ADDRESS=\([^ #]*\).*/\1/p' "$HERE/.env.production" 2>/dev/null)"
[ -n "$SITE" ] || { curl -fsS -m 10 --retry 2 -o /dev/null "$HEALTHCHECK_URL/fail" --data-raw "SITE_ADDRESS not found in .env.production" ; exit 1; }

if curl -fsS -m 10 -o /dev/null "https://$SITE/readyz"; then
  curl -fsS -m 10 --retry 3 -o /dev/null "$HEALTHCHECK_URL"
else
  curl -fsS -m 10 --retry 3 -o /dev/null "$HEALTHCHECK_URL/fail" --data-raw "https://$SITE/readyz did not answer"
  exit 1
fi
