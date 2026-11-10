#!/usr/bin/env bash
# Validates HawkEye's own alerting configuration with the same pinned images production runs (deploy/docker-compose.monitoring.yml):
#
#   scripts/check-alerting.sh
#
#   * promtool: deploy/prometheus/prometheus.yml and the rules it loads (deploy/prometheus-alerts.yml) are valid,
#   * promtool: the unit tests in deploy/prometheus/alerting_test.yml pass (the watchdog, unreachable and missing metrics),
#   * deploy/alertmanager/entrypoint.sh renders a configuration amtool accepts with every combination of settings, and refuses malformed ones.
#
# Needs only docker. CI runs it (.github/workflows/ci.yml); run it after changing any of those files.
set -euo pipefail
cd "$(dirname "$0")/.."
DEPLOY="$PWD/deploy"
IMAGES="$(sed -n 's/^ *image: \(prom\/[a-z]*:v[0-9.]*\)$/\1/p' deploy/docker-compose.monitoring.yml)"
PROM="$(grep '^prom/prometheus:' <<< "$IMAGES")"
AM="$(grep '^prom/alertmanager:' <<< "$IMAGES")"
[ -n "$PROM" ] && [ -n "$AM" ] || { echo "could not find the Prometheus and Alertmanager images in deploy/docker-compose.monitoring.yml" >&2; exit 1; }
fail=0

echo "== Prometheus configuration and rules ($PROM)"
docker run --rm -v "$DEPLOY:/deploy:ro" -v "$DEPLOY/prometheus-alerts.yml:/etc/prometheus/rules/hawkeye.yml:ro" --entrypoint promtool "$PROM" \
  check config /deploy/prometheus/prometheus.yml || fail=1
echo "== Rule unit tests"
docker run --rm -v "$DEPLOY:/deploy:ro" --entrypoint promtool "$PROM" test rules /deploy/prometheus/alerting_test.yml || fail=1

# entrypoint.sh with its last line replaced: check the rendered file instead of starting Alertmanager.
render() { # expect(ok|refused) name env...
  local expect="$1" name="$2"; shift 2
  local env=() e out rc=0
  for e in "$@"; do env+=(-e "$e"); done
  out="$(docker run --rm --read-only --tmpfs /tmp ${env[@]+"${env[@]}"} -v "$DEPLOY/alertmanager:/etc/alertmanager:ro" --entrypoint /bin/sh "$AM" -c \
    'sed "s#^exec /bin/alertmanager.*#exec amtool check-config \$OUT/alertmanager.yml#" /etc/alertmanager/entrypoint.sh > /tmp/check.sh && sh /tmp/check.sh' 2>&1)" || rc=$?
  if { [ "$expect" = ok ] && [ "$rc" -eq 0 ]; } || { [ "$expect" = refused ] && [ "$rc" -ne 0 ] && ! grep -q "Checking" <<< "$out"; }; then
    echo "  ok      $name"
  else
    echo "  FAILED  $name (expected $expect, exit $rc)"; sed 's/^/          /' <<< "$out"; fail=1
  fi
}
echo "== Alertmanager configuration, rendered with each combination of settings ($AM)"
render ok      "everything set"            ALERTS_TELEGRAM_BOT_TOKEN=123456:AAH-x_y ALERTS_TELEGRAM_CHAT_ID=-1001234 ALERTS_WATCHDOG_URL=https://hc-ping.com/0000
render ok      "nothing set"
render ok      "Telegram only"             ALERTS_TELEGRAM_BOT_TOKEN=123456:AAH ALERTS_TELEGRAM_CHAT_ID=42
render ok      "watchdog only"             ALERTS_WATCHDOG_URL=https://hc-ping.com/0000
render ok      "customer bot as fallback"  TELEGRAM_BOT_TOKEN=999:BBB ALERTS_TELEGRAM_CHAT_ID=7
render refused "chat id not a number"      ALERTS_TELEGRAM_BOT_TOKEN=123456:AAH ALERTS_TELEGRAM_CHAT_ID=4-2
render refused "malformed bot token"       "ALERTS_TELEGRAM_BOT_TOKEN=12 3:A" ALERTS_TELEGRAM_CHAT_ID=42
render refused "watchdog URL not https"    ALERTS_WATCHDOG_URL=http://hc-ping.com/0000

[ "$fail" -eq 0 ] && echo "== alerting configuration OK" || { echo "!! alerting configuration has problems (above)" >&2; exit 1; }
