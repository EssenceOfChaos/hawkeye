#!/usr/bin/env bash
# Takes a release from the production server and starts it. Runs on a regional worker as the forced command of the production server's key, so this script is
# all that key can do here. The release's commit arrives as the SSH command, and a tar of image.tar.gz, worker.env and heartbeat.url arrives on stdin.
#
# If the new release does not become healthy, the worker goes back to what it was running and this exits non-zero, which is how the production server finds out.
set -euo pipefail
# Everything lives in one function that is parsed before it runs: the checkout below replaces this very file, and bash must not read a half-changed script.
main() {
  local version="${SSH_ORIGINAL_COMMAND:-}" previous_commit rc
  [[ "$version" =~ ^[0-9a-f]{7,40}$ ]] || { echo "refusing: '$version' is not a commit" >&2; exit 2; }
  cd "${HAWKEYE_ROOT:-$HOME/hawkeye}"
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
