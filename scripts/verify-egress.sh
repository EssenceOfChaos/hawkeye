#!/usr/bin/env bash
# Verifies that containers on the worker network can reach the public internet but NOT the docker host or private/metadata addresses.
#
#   scripts/verify-egress.sh [NETWORK]          # default network: hawkeye_checks (docker compose project "hawkeye")
#
# Method: a throwaway container on the worker network tries each target. The host-service probe has a POSITIVE CONTROL: this script starts a real
# listener on the host and first proves it is reachable from a container on an ordinary network, so a "blocked" result can only mean the
# firewall blocked it (not that nothing was listening). Run it on the real deployment host after deploy/egress-filter.sh apply.
set -u
NET="${1:-hawkeye_checks}"
PORT="${VERIFY_PORT:-18999}"
IMG="curlimages/curl:8.11.1"
pass=0; fail=0
ok()  { printf '  \033[32mPASS\033[0m %s\n' "$1"; pass=$((pass+1)); }
bad() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail+1)); }
probe() { # network url -> exit 0 if connection succeeded (any HTTP response)
  docker run --rm --network "$1" --add-host host.docker.internal:host-gateway "$IMG" -sS -o /dev/null --max-time 5 "$2" >/dev/null 2>&1
}
docker network inspect "$NET" >/dev/null 2>&1 || { echo "network $NET not found (is the stack up?)" >&2; exit 2; }

# A listener on the host, reachable on all interfaces.
python3 -m http.server "$PORT" --bind 0.0.0.0 >/dev/null 2>&1 &
LISTENER=$!; trap 'kill $LISTENER 2>/dev/null' EXIT; sleep 1

echo "Egress verification on network $NET"
echo "== positive controls (must succeed, otherwise the checks below prove nothing)"
if probe bridge "http://host.docker.internal:$PORT/"; then ok "host listener reachable from an ordinary container"; else bad "host listener not reachable from a normal container: cannot run the host-service check"; fi
if probe "$NET" "https://example.com/"; then ok "public internet reachable from the worker network"; else bad "worker network cannot reach the public internet"; fi

echo "== must be blocked"
if probe "$NET" "http://host.docker.internal:$PORT/"; then bad "worker network CAN reach a service on the docker host"; else ok "docker host services are unreachable from the worker network"; fi
for target in "http://169.254.169.254/latest/meta-data/" "http://10.0.0.1/" "http://192.168.1.1/" "http://172.17.0.1/" "http://100.100.100.200/"; do
  if probe "$NET" "$target"; then bad "reached $target"; else ok "blocked $target"; fi
done

echo; echo "Result: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
