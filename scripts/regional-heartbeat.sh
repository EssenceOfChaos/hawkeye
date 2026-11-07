#!/usr/bin/env bash
# Dead-man's switch for a regional worker: run every minute from cron. The worker's health check needs the database, so a ping means the host, the
# container and the tunnel are all working. If the host dies, the pings stop and healthchecks.io alerts you when the grace period passes.
# The production server creates each worker's check and sends its address here (scripts/regional-sync.sh). Without one, this does nothing.
set -uo pipefail
ENV_FILE="${HAWKEYE_REGION_HEARTBEAT:-$HOME/.hawkeye-region-heartbeat}"
[ -f "$ENV_FILE" ] || exit 0
# shellcheck disable=SC1090
. "$ENV_FILE"
[ -n "${HEALTHCHECK_URL:-}" ] || exit 0
state="$(docker inspect -f '{{.State.Health.Status}}' hawkeye-worker-1 2>/dev/null || echo 'not running')"
if [ "$state" = healthy ]; then
  curl -fsS -m 10 --retry 3 -o /dev/null "$HEALTHCHECK_URL"
else
  curl -fsS -m 10 --retry 3 -o /dev/null "$HEALTHCHECK_URL/fail" --data-raw "worker is $state"
  exit 1
fi
