#!/usr/bin/env bash
# The HawkEye deploy action (action.yml). Usable on its own too: set the HAWKEYE_* variables below and run it.
#   HAWKEYE_KEY    a deploy key (required)
#   HAWKEYE_STEP   start | finish | check (required)
#   HAWKEYE_TAG, HAWKEYE_MINUTES, HAWKEYE_NOTE, HAWKEYE_VERIFY (true|false), HAWKEYE_WAIT (seconds), HAWKEYE_URL
set -euo pipefail

url="${HAWKEYE_URL:-https://hawkeye.observer}"
url="${url%/}/api/v1"
wait="${HAWKEYE_WAIT:-60}"
: "${HAWKEYE_KEY:?set the key input to a HawkEye deploy key}"
# Keep the key out of the logs, whatever happens below.
[ -n "${GITHUB_ACTIONS:-}" ] && echo "::add-mask::$HAWKEYE_KEY"

summary() { [ -n "${GITHUB_STEP_SUMMARY:-}" ] && printf '%s\n' "$1" >> "$GITHUB_STEP_SUMMARY" || true; }
# message prints the "message" field of a JSON answer (or the whole answer when it has none, or jq is missing).
message() {
  if command -v jq >/dev/null 2>&1; then
    jq -r '.message // .error.message // empty' <<<"$1" 2>/dev/null || printf '%s\n' "$1"
  else
    printf '%s\n' "$1"
  fi
}

# call METHOD PATH [curl args...]: sets $code and $body.
call() {
  local method=$1 path=$2
  shift 2
  local out
  out=$(curl -sS -X "$method" "$url$path" -H "Authorization: Bearer $HAWKEYE_KEY" -w '\n%{http_code}' "$@") || { code=000; body="Couldn't reach HawkEye at $url"; return 0; }
  code=${out##*$'\n'}
  body=${out%$'\n'*}
}

# checked PATH [curl args...]: calls a finish or check, retrying a failed check every 10 seconds for up to $wait seconds.
checked() {
  local path=$1 deadline=$((SECONDS + wait))
  shift
  while :; do
    call POST "$path" "$@"
    if [ "$code" = 200 ]; then
      message "$body"
      summary "✅ HawkEye: $(message "$body")"
      return 0
    fi
    # Only a failing site is worth waiting for; anything else (a bad key, no deploy) won't get better.
    if [ "$code" != 422 ] || [ $SECONDS -ge "$deadline" ]; then
      echo "::error title=HawkEye::$(message "$body")"
      summary "❌ HawkEye: $(message "$body")"
      return 1
    fi
    echo "Not healthy yet: $(message "$body") Trying again in 10 seconds."
    sleep 10
  done
}

case "${HAWKEYE_STEP:-}" in
  start)
    args=(--data-urlencode "minutes=${HAWKEYE_MINUTES:-15}")
    [ -n "${HAWKEYE_TAG:-}" ] && args+=(--data-urlencode "tag=$HAWKEYE_TAG")
    [ -n "${HAWKEYE_NOTE:-}" ] && args+=(--data-urlencode "note=${HAWKEYE_NOTE:0:200}")
    call POST /deploys ${args[@]+"${args[@]}"}
    if [ "$code" != 200 ] && [ "$code" != 201 ]; then
      echo "::error title=HawkEye::$(message "$body")"
      exit 1
    fi
    echo "HawkEye: alerts are quiet for up to ${HAWKEYE_MINUTES:-15} minutes while you deploy."
    ;;
  finish)
    if [ "${HAWKEYE_VERIFY:-true}" = false ]; then
      call POST "/deploys/latest/finish?verify=false"
      [ "$code" = 200 ] || { echo "::error title=HawkEye::$(message "$body")"; exit 1; }
      message "$body"
    else
      checked /deploys/latest/finish
    fi
    ;;
  check)
    args=()
    [ -n "${HAWKEYE_TAG:-}" ] && args+=(--data-urlencode "tag=$HAWKEYE_TAG")
    checked /deploys/check ${args[@]+"${args[@]}"}
    ;;
  *)
    echo "::error title=HawkEye::step must be start, finish or check"
    exit 1
    ;;
esac
