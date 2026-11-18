#!/usr/bin/env bash
# What the production server's key may do on a regional worker: it runs as that key's forced command (installed by the worker's first boot,
# infra/worker-cloud-init.yaml.tftpl), so this script is all the key can do here. The SSH command is one of:
#
#   COMMIT                 take a release and start it: a tar of image.tar.gz, worker.env and heartbeat.url arrives on stdin
#   ready                  succeed once this host's first boot has finished (scripts/regional-link.sh waits for it)
#   link-key               print this host's WireGuard public key, making the key the first time
#   link-apply HUB_PUBKEY  join the tunnel to the production server; its address and this host's tunnel address come from the first boot
#
# If a new release does not become healthy, the worker goes back to what it was running and this exits non-zero, which is how the production server finds out.
set -euo pipefail
REGION_ENV="${HAWKEYE_REGION_ENV:-/etc/hawkeye-region.env}"
FIRSTBOOT="${HAWKEYE_FIRSTBOOT:-/var/lib/hawkeye-firstboot-done}"
# One value from the first boot's file, read rather than sourced.
region_value() { sed -n "s/^$1=\\([0-9.]*\\)\$/\\1/p" "$REGION_ENV" | tail -1; }
# Everything lives in one function that is parsed before it runs: the checkout below replaces this very file, and bash must not read a half-changed script.
main() {
  local version="${SSH_ORIGINAL_COMMAND:-}" previous_commit rc
  cd "${HAWKEYE_ROOT:-$HOME/hawkeye}"
  case "$version" in
    ready) [ -f "$FIRSTBOOT" ]; exit $? ;;
    link-key) exec sudo -n bash deploy/wireguard.sh worker-key ;;
    "link-apply "*)
      local hub_pub="${version#link-apply }" hub_ip tunnel_ip
      hub_ip="$(region_value HUB_IP)"; tunnel_ip="$(region_value TUNNEL_IP)"
      [ -n "$hub_ip" ] && [ -n "$tunnel_ip" ] || { echo "refusing: $REGION_ENV has no HUB_IP or TUNNEL_IP" >&2; exit 2; }
      # wireguard.sh checks the key and both addresses before it writes anything.
      exec sudo -n bash deploy/wireguard.sh worker-apply "$hub_pub" "$hub_ip" "$tunnel_ip" ;;
  esac
  [[ "$version" =~ ^[0-9a-f]{7,40}$ ]] || { echo "refusing: '$version' is not a commit" >&2; exit 2; }
  exec 9>"$HOME/.regional.lock"
  flock -n 9 || { echo "another release is being received" >&2; exit 1; }
  umask 077
  WORK="$(mktemp -d "$HOME/.regional-receive.XXXXXX")"   # not local: the trap runs as the script ends
  trap 'rm -rf "$WORK"' EXIT

  tar -xf - -C "$WORK"
  gunzip -c "$WORK/image.tar.gz" | docker load >/dev/null

  compose() { docker compose --env-file .env.worker -f deploy/docker-compose.worker.yml "$@"; }
  healthy() {
    local i state
    for i in $(seq 1 "${RECEIVE_TRIES:-30}"); do
      state="$(docker inspect -f '{{.State.Health.Status}}' hawkeye-worker-1 2>/dev/null || true)"
      [ "$state" = healthy ] && return 0
      sleep "${RECEIVE_WAIT:-3}"
    done
    echo "the worker is ${state:-not running}" >&2; return 1
  }

  previous_commit="$(git rev-parse HEAD)"
  [ ! -f .env.worker ] || cp -p .env.worker .env.worker.previous
  git fetch --quiet origin main
  git reset --quiet --hard "$version"
  { cat "$WORK/worker.env"; echo "APP_VERSION=$version"; } > .env.worker.new
  mv .env.worker.new .env.worker
  if [ -s "$WORK/heartbeat.url" ]; then echo "HEALTHCHECK_URL=$(cat "$WORK/heartbeat.url")" > "$HOME/.hawkeye-region-heartbeat"; fi

  rc=0
  compose up -d --remove-orphans || rc=$?
  if [ "$rc" -eq 0 ] && healthy; then
    # Keep this release and the one before it, for going back.
    local keep="" tag
    [ ! -f .env.worker.previous ] || keep="$(sed -n 's/^APP_VERSION=//p' .env.worker.previous | tail -1)"
    for tag in $(docker images hawkeye-worker --format '{{.Tag}}'); do
      [ "$tag" = "$version" ] || [ "$tag" = "$keep" ] || docker rmi "hawkeye-worker:$tag" >/dev/null || true
    done
    echo "healthy on ${version:0:12}"
    exit 0
  fi

  echo "!! release ${version:0:12} did not start here" >&2
  docker logs --tail 20 hawkeye-worker-1 >&2 2>&1 || true
  if [ -f .env.worker.previous ]; then
    git reset --quiet --hard "$previous_commit"
    cp -p .env.worker.previous .env.worker
    compose up -d --remove-orphans || true
    if healthy; then echo "!! back on the previous release" >&2; else echo "!! THE PREVIOUS RELEASE DID NOT START EITHER" >&2; fi
  fi
  exit 1
}
main "$@"
exit $?
