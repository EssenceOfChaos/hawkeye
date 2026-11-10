#!/bin/sh
# Starts Alertmanager for HawkEye's own alerts (deploy/docker-compose.monitoring.yml). Alertmanager cannot read settings from the environment, so
# this renders alertmanager.yml.tmpl into /tmp (a tmpfs) at every start, with the secrets in files only Alertmanager's user can read.
#
#   ALERTS_TELEGRAM_BOT_TOKEN  a bot from @BotFather for these alerts (falls back to TELEGRAM_BOT_TOKEN, the customer-facing bot)
#   ALERTS_TELEGRAM_CHAT_ID    the chat they go to: a number, negative for a group
#   ALERTS_WATCHDOG_URL        a healthchecks.io ping URL that Alertmanager pings about every minute (docs/operations.md#alerting-and-external-monitoring)
#
# A missing setting does not stop Alertmanager: it starts without that receiver and says so in its log, so the rest keeps working. Settings that
# are present but malformed stop it, because a typo must not look like a working pager.
set -eu
umask 077
OUT=/tmp/alertmanager
mkdir -p "$OUT"
TOKEN="${ALERTS_TELEGRAM_BOT_TOKEN:-${TELEGRAM_BOT_TOKEN:-}}"
CHAT="${ALERTS_TELEGRAM_CHAT_ID:-}"
WATCHDOG="${ALERTS_WATCHDOG_URL:-}"
drop=""

if [ -n "$TOKEN" ] && [ -n "$CHAT" ]; then
  case "$TOKEN" in *[!0-9A-Za-z:_-]* | :* | *: | [!0-9]*) echo "alertmanager: the Telegram bot token is malformed (expected digits, a colon, then letters, digits, _ and -)" >&2; exit 1 ;; esac
  case "$CHAT" in -) echo "alertmanager: ALERTS_TELEGRAM_CHAT_ID must be a number" >&2; exit 1 ;; *[!0-9-]* | ?*-*) echo "alertmanager: ALERTS_TELEGRAM_CHAT_ID must be a number (negative for a group)" >&2; exit 1 ;; esac
  printf '%s' "$TOKEN" > "$OUT/telegram_token"
  echo "alertmanager: page and ticket alerts go to Telegram chat $CHAT"
else
  echo "alertmanager: WARNING ALERTS_TELEGRAM_CHAT_ID or the bot token is not set: alerts are evaluated but NOT DELIVERED to anyone" >&2
  drop="$drop telegram"
  CHAT=0
fi

if [ -n "$WATCHDOG" ]; then
  case "$WATCHDOG" in https://*) ;; *) echo "alertmanager: ALERTS_WATCHDOG_URL must be an https:// URL" >&2; exit 1 ;; esac
  printf '%s' "$WATCHDOG" > "$OUT/watchdog_url"
  echo "alertmanager: the watchdog pings healthchecks.io"
else
  echo "alertmanager: WARNING ALERTS_WATCHDOG_URL is not set: nothing outside this server will notice if alerting itself stops" >&2
  drop="$drop watchdog"
fi

awk -v drop=" $drop " -v chat="$CHAT" '
  /^[[:space:]]*#@begin / { skipping = index(drop, " " $2 " ") > 0; next }
  /^[[:space:]]*#@end/    { skipping = 0; next }
  skipping                { next }
  { gsub(/__CHAT_ID__/, chat); print }
' /etc/alertmanager/alertmanager.yml.tmpl > "$OUT/alertmanager.yml"

# One instance: no cluster gossip port.
exec /bin/alertmanager --config.file="$OUT/alertmanager.yml" --storage.path=/alertmanager --cluster.listen-address= "$@"
