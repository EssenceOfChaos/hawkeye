#!/usr/bin/env bash
# HawkEye's own alerts on the production server (deploy/docker-compose.monitoring.yml): prove the pager works, see what fires, acknowledge a page.
#
#   scripts/alerts.sh test [page|ticket]    send a test alert through Alertmanager to Telegram (page: with sound; it resolves by itself in ~5 min)
#   scripts/alerts.sh list                  alerts firing now (the Watchdog always is: that is how healthchecks.io knows alerting works)
#   scripts/alerts.sh silence NAME [2h]     acknowledge: stop NAME's repeats for a while (it still resolves, and fires again after the silence)
#   scripts/alerts.sh silences              current silences
#   scripts/alerts.sh unsilence ID          end a silence early
#   scripts/alerts.sh targets               what Prometheus scrapes, and whether each answers
#
# Run on the server as the deploy user. Nothing here is reachable from outside: it talks to the containers with docker exec.
# After setting up or changing ALERTS_TELEGRAM_* (docs/operations.md#alerting-and-external-monitoring), run `test page` and check your phone.
set -euo pipefail
AM="${ALERTMANAGER_CONTAINER:-hawkeye-alertmanager-1}"
PROM="${PROMETHEUS_CONTAINER:-hawkeye-prometheus-1}"
amtool() { docker exec "$AM" amtool --alertmanager.url=http://localhost:9093 "$@"; }
running() { [ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = true ] || { echo "$1 is not running (is deploy/docker-compose.monitoring.yml part of the stack?)" >&2; exit 1; }; }

case "${1:-}" in
  test)
    sev="${2:-page}"
    case "$sev" in page|ticket) ;; *) echo "usage: $0 test [page|ticket]" >&2; exit 2 ;; esac
    running "$AM"
    amtool alert add alertname=HawkEyeTestAlert "severity=$sev" \
      "--annotation=summary=\"Test $sev from scripts/alerts.sh on $(hostname): if this reached you, HawkEye's own alerting works\""
    echo "sent a test $sev. It should reach Telegram within about 30 seconds and resolve by itself in about 5 minutes."
    echo "Nothing arrived? docker logs --tail 50 $AM  (a wrong token or chat id is logged there)"
    ;;
  list)
    running "$AM"; amtool alert query ;;
  silence)
    name="${2:?usage: $0 silence NAME [duration, default 2h]}"; dur="${3:-2h}"
    running "$AM"
    id="$(amtool silence add "alertname=$name" "--duration=$dur" "--author=${USER:-deploy}" "--comment=acknowledged with scripts/alerts.sh")"
    echo "silenced $name for $dur (silence $id; end it early with: $0 unsilence $id)" ;;
  silences)
    running "$AM"; amtool silence query ;;
  unsilence)
    running "$AM"; amtool silence expire "${2:?usage: $0 unsilence ID}" && echo "silence ${2} ended" ;;
  targets)
    running "$PROM"
    docker exec "$PROM" wget -qO- http://localhost:9090/api/v1/targets | python3 -c '
import json, sys
bad = 0
for t in json.load(sys.stdin)["data"]["activeTargets"]:
    bad += t["health"] != "up"
    print("  %-8s%-14s%-40s%s" % (t["health"], t["labels"]["job"], t["scrapeUrl"], t.get("lastError") or ""))
sys.exit(1 if bad else 0)' ;;
  *)
    sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac
