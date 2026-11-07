#!/usr/bin/env bash
# Sends this release and its settings to every regional worker. Runs on the production server, from scripts/deploy.sh after a healthy deploy, a rollback or
# a change of settings, so nobody has to remember to run it.
#
#   scripts/regional-sync.sh
#
# Workers are reached through the tunnel with a key that can do one thing on them: run scripts/regional-receive.sh. Exit status: 0 every linked worker is on
# this release (or there are none), 1 at least one is not. A failure here never fails or rolls back a production deploy: scripts/deploy.sh carries on, and
# healthchecks.io alerts you through the "HawkEye regional deploys" check when HEALTHCHECKS_API_KEY is set. A worker that cannot start a release goes back
# to the one it was running, by itself.
#
# Only the values in SHARED reach a worker: never SMTP, Stripe, the session secret or the secret manager's credentials.
set -uo pipefail
cd "$(dirname "$0")/.."
SHARED='DATABASE_URL|SECRETS_ENCRYPTION_KEY|SENTRY_DSN|LOG_LEVEL|WORKER_CONCURRENCY|CANARY_URLS|CANARY_INTERVAL'
HUB_TUNNEL_IP=10.66.0.1
KEY="${HAWKEYE_REGIONAL_KEY:-$HOME/.ssh/hawkeye-regional}"
HOST_ENV="${HAWKEYE_HOST_ENV:-$HOME/.hawkeye-host.env}"
SYNC_CHECK="HawkEye regional deploys"
# The tunnel proves who is at the other end: a tunnel address belongs to one WireGuard key. A replaced worker has a new host key but the same address,
# so a host key check here would only ever fail on a legitimate replacement.
SSH=(ssh -i "$KEY" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=10 -o ServerAliveInterval=15 -o ServerAliveCountMax=4)

WORKERS="$(python3 scripts/regions.py workers)" || { echo "regional: infra/regions.json is not valid" >&2; exit 1; }
[ -n "$WORKERS" ] || exit 0   # the usual case until a worker is listed

# A worker is listed before it exists: the pull request comes first, then `scripts/rebuild.sh regions`. Until it is linked, there is nothing to send it to.
linked() { [ -f "$KEY" ] && sudo -n wg show wg0 allowed-ips 2>/dev/null | grep -qF "$1/32"; }

# shellcheck disable=SC1090
[ -f "$HOST_ENV" ] && { set -a; . "$HOST_ENV"; set +a; }
ping_url() { python3 scripts/regions.py healthcheck "$@" 2>/dev/null || true; }

VERSION="$(sed -n 's/^APP_VERSION=//p' .env.production | tail -1)"
[[ "$VERSION" =~ ^[0-9a-f]{7,40}$ ]] || { echo "regional: APP_VERSION is '$VERSION', not a commit" >&2; exit 1; }
SRC="$(grep -E "^($SHARED)=" .env.production)"
grep -q '^DATABASE_URL=.*@db:5432/' <<<"$SRC" || { echo "regional: DATABASE_URL does not use db:5432, so the tunnel's forwarding would not reach it" >&2; exit 1; }
SRC="${SRC//@db:5432\//@$HUB_TUNNEL_IP:5432/}"

umask 077
WORK="$(mktemp -d "$HOME/.regional-sync.XXXXXX")" || exit 1
trap 'rm -rf "$WORK"' EXIT

failed=""; sent=0; image=""
while read -r code tunnel_ip; do
  if ! linked "$tunnel_ip"; then echo "regional: $code is listed but not linked yet: skipping it (scripts/rebuild.sh regions)"; continue; fi
  if [ -z "$image" ]; then
    docker save "hawkeye-worker:$VERSION" | gzip > "$WORK/image.tar.gz" || { echo "regional: cannot read the image for $VERSION" >&2; exit 1; }
    image=done
  fi
  dir="$WORK/$code"; mkdir "$dir"
  ln "$WORK/image.tar.gz" "$dir/image.tar.gz"
  # ALLOW_INSECURE_DATABASE: the database has no TLS. The tunnel encrypts this connection from end to end instead.
  printf "%s\nALLOW_INSECURE_DATABASE=true\nCHECK_REGIONS='%s'\nWORKER_REGION=%s\n" "$SRC" "$(python3 scripts/regions.py worker-check-regions "$code")" "$code" > "$dir/worker.env"
  ping_url "HawkEye worker $code" 60 300 > "$dir/heartbeat.url"
  echo "== regional: $code ($tunnel_ip): release ${VERSION:0:12}"
  if tar -cf - -C "$dir" . | "${SSH[@]}" "deploy@$tunnel_ip" "$VERSION"; then sent=$((sent+1)); else failed="$failed $code"; fi
  rm -rf "$dir"
done <<<"$WORKERS"

SYNC_URL="$(ping_url "$SYNC_CHECK" 31536000 60)"   # it reports failures: only a deploy pings it, so it must never time out by itself
if [ -n "$failed" ]; then
  echo "!! regional: not updated:$failed. They keep running the release they had" >&2
  [ -z "$SYNC_URL" ] || curl -fsS -m 10 --retry 2 -o /dev/null "$SYNC_URL/fail" --data-raw "${VERSION:0:12} did not reach:$failed" || true
  exit 1
fi
[ -z "$SYNC_URL" ] || curl -fsS -m 10 --retry 2 -o /dev/null "$SYNC_URL" || true
[ "$sent" -eq 0 ] || echo "== regional: $sent worker(s) on ${VERSION:0:12}"
