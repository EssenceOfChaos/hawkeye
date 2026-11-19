#!/usr/bin/env bash
# Deploys a commit of this repo on the production host: back up, build, migrate, restart, smoke-test, and roll back if the smoke test fails.
#
#   scripts/deploy.sh [COMMIT_SHA]        # default: the tip of origin/main
#   scripts/deploy.sh secrets-only        # no new code: refresh the settings from the secret manager and restart what changed
#   scripts/deploy.sh regions CODE=PUBLIC_IP,TUNNEL_IP ...
#                                         # no new code: link these regional workers (every one OpenTofu has) and send them this release
#
# Run on the VM as the deploy user (from the repo root) or over SSH by the GitHub Actions workflow (.github/workflows/deploy.yml). When the
# SSH key is restricted with a forced command (docs/deployment.md), the sha (or "secrets-only", or "regions ...") arrives in $SSH_ORIGINAL_COMMAND. Exit 0 means
# the new version is live and healthy; non-zero means it was rolled back (or never started) and the log says why.
#
# Settings: when this machine has credentials for the secret manager (~/.hawkeye-infisical.env), every release first refreshes .env.production
# and ~/.hawkeye-host.env from it (scripts/sync-secrets.sh; docs/secrets.md). If it cannot be reached, the release carries on with the settings
# already on the server and says so. Without credentials, the existing .env.production is used as it is.
set -euo pipefail
# Everything lives in one function that is parsed before it runs: the checkout below replaces this very file, and bash must not read a
# half-changed script. So the release that changes this file is deployed by the previous copy; when that release is healthy, the new copy is run
# once for the same commit, so whatever it adds takes effect without a second deploy by hand (docs/deployment.md).
main() {
  cd "$(dirname "$0")/.."
  ROOT="$PWD"

  TARGET="${*:-${SSH_ORIGINAL_COMMAND:-}}"
  # "regions" is followed by the workers OpenTofu has, from the Infrastructure workflow: CODE=PUBLIC_IP,TUNNEL_IP each. scripts/regional-link.sh
  # checks every value again; this only keeps anything else off the command line.
  local worker_re='[a-z0-9][a-z0-9-]{0,18}[a-z0-9]=([0-9]{1,3}\.){3}[0-9]{1,3},10\.66\.0\.[0-9]{1,3}'
  if [ -n "$TARGET" ] && [ "$TARGET" != "secrets-only" ] && ! [[ "$TARGET" =~ ^[0-9a-f]{40}$ ]] && ! [[ "$TARGET" =~ ^regions(\ $worker_re)*$ ]]; then
    echo "refusing: '$TARGET' is not a full commit sha (or secrets-only, or regions CODE=PUBLIC_IP,TUNNEL_IP ...)" >&2; exit 2
  fi

  exec 9>"$ROOT/.deploy.lock"
  if [[ "$TARGET" == regions* ]]; then
    # Runs right after an infrastructure apply, often while the release from the same merge is deploying: wait for it rather than fail.
    flock -w "${REGIONS_LOCK_WAIT:-1800}" 9 || { echo "a deploy has held the lock too long: try the Infrastructure workflow again" >&2; exit 1; }
  else
    flock -n 9 || { echo "another deploy is running" >&2; exit 1; }
  fi
  # This copy, as parsed: compared with the checked-out one after a healthy release.
  SELF_SUM="$(sha256sum scripts/deploy.sh | cut -d' ' -f1)"

  # Our own alerting (Prometheus and Alertmanager) comes with the release that has it: a rollback to an older release, without the file, runs
  # without it (and --remove-orphans stops it) rather than failing.
  compose() {
    local files=(-f deploy/docker-compose.prod.yml -f deploy/docker-compose.local-db.yml)
    [ ! -f deploy/docker-compose.monitoring.yml ] || files+=(-f deploy/docker-compose.monitoring.yml)
    docker compose --env-file .env.production "${files[@]}" "$@"
  }
  [ -f .env.production ] || { echo ".env.production is missing" >&2; exit 1; }
  SITE="$(sed -n 's/^SITE_ADDRESS=\([^ #]*\).*/\1/p' .env.production)"
  [ -n "$SITE" ] || { echo "SITE_ADDRESS not found in .env.production" >&2; exit 1; }

  # Check locations come from infra/regions.json in the release itself, never from the secret manager, so a release and its regions move together.
  apply_regions() { [ ! -f scripts/regions.py ] || python3 scripts/regions.py apply-env .env.production; }
  # Sends this release and its settings to the regional workers. A problem with a worker never fails production: it stays on the release it had and an
  # alert goes out (scripts/regional-sync.sh). The script is missing when rolling back to a release from before it existed.
  update_regions() {
    [ ! -x scripts/regional-link.sh ] || scripts/regional-link.sh --key || true   # says so if new workers would not trust this server
    [ ! -x scripts/regional-link.sh ] || scripts/regional-link.sh --known || true   # this release's tunnel rules, for the workers linked already
    [ ! -x scripts/regional-sync.sh ] || scripts/regional-sync.sh || echo "!! not every regional worker was updated; production is fine" >&2
  }
  healthy() { # smoke test against the public URL, retrying while containers settle
    local i
    for i in $(seq 1 "${HEALTH_TRIES:-6}"); do   # HEALTH_TRIES and HEALTH_WAIT exist so the tests need not wait a minute
      if scripts/smoke.sh "https://$SITE" >/tmp/hawkeye-smoke.log 2>&1; then return 0; fi
      sleep "${HEALTH_WAIT:-10}"
    done
    cat /tmp/hawkeye-smoke.log; return 1
  }
  release() { # sha [keep]: check out the code, refresh the settings (unless "keep"), label the release, build, migrate, start
    local sha="$1" settings="${2:-refresh}" rc=0
    git checkout --quiet main && git reset --quiet --hard "$sha"   # stay on the main branch so a manual `git pull` keeps working
    if [ "$settings" = refresh ]; then
      # The scripts and manifest of the release being deployed, so the settings it needs are checked by the code that needs them.
      scripts/sync-secrets.sh || rc=$?
      case "$rc" in
        0) ;;
        10) echo "!! the secret manager is unreachable: deploying with the settings already on this server" >&2 ;;
        *) echo "!! the settings were not refreshed (exit $rc), so this release is not started" >&2; return 1 ;;
      esac
    fi
    apply_regions || return 1
    sed -i '/^APP_VERSION=/d' .env.production; echo "APP_VERSION=${sha:0:12}" >> .env.production   # shown in logs and Sentry; also tags the images
    compose up -d --build --remove-orphans || return 1
    # Prometheus and Alertmanager read their rules and templates once, at start, and a running container keeps seeing the file a checkout replaced
    # (single-file bind mounts follow the old inode). Restart them so this release's alert rules are the ones evaluated. Not a reason to roll back.
    [ ! -f deploy/docker-compose.monitoring.yml ] || compose restart prometheus alertmanager >/dev/null ||
      echo "!! could not restart Prometheus and Alertmanager: they may still use the previous release's alert rules" >&2
  }

  # The settings as they are now: what a rollback puts back (a new release may bring new settings, and they are part of what failed).
  cp -p .env.production .env.production.previous

  if [[ "$TARGET" == regions* ]]; then
    # No new code and no settings change: join the workers to the tunnel, then send them the release this server is running.
    local workers rc=0
    read -ra workers <<<"${TARGET#regions}"
    [ -x scripts/regional-link.sh ] || { echo "this release has no scripts/regional-link.sh: deploy a newer one first" >&2; exit 1; }
    scripts/regional-link.sh "${workers[@]}" || rc=$?
    [ "${#workers[@]}" -eq 0 ] || scripts/regional-sync.sh || rc=1
    exit "$rc"
  fi

  if [ "$TARGET" = "secrets-only" ]; then
    echo "== refreshing settings from the secret manager (no new code)"
    rc=0; scripts/sync-secrets.sh || rc=$?
    if [ "$rc" -ne 0 ]; then echo "!! settings were not refreshed (exit $rc): nothing was changed" >&2; exit 1; fi
    apply_regions
    # The header comment carries the time it was written, so compare the settings themselves.
    if diff -q <(grep -v '^#' .env.production) <(grep -v '^#' .env.production.previous) >/dev/null; then echo "== the settings have not changed: nothing to restart"; exit 0; fi
    compose up -d --remove-orphans
    if healthy; then echo "== settings applied: smoke test passed"; update_regions; exit 0; fi
    echo "!! the site is unhealthy with the new settings; putting the previous ones back" >&2
    cp -p .env.production.previous .env.production
    compose up -d --remove-orphans
    if healthy; then echo "!! rolled back: the previous settings are live again. The new settings did not go out." >&2
    else echo "!! ROLLBACK ALSO FAILED: the site needs attention (docker compose logs on the VM)" >&2; fi
    exit 1
  fi

  PREVIOUS="$(git rev-parse HEAD)"
  git fetch --quiet origin main
  [ -n "$TARGET" ] || TARGET="$(git rev-parse origin/main)"
  git cat-file -e "$TARGET^{commit}" 2>/dev/null || { echo "commit $TARGET is not in the repository (not pushed?)" >&2; exit 1; }
  echo "== deploying ${TARGET:0:12} (currently ${PREVIOUS:0:12}) to https://$SITE"

  # 1. A backup first: migrations are forward-only, so this is the way back from a bad one. Skipped only if off-host backups are not set up yet.
  if [ -f "$HOME/.hawkeye-host.env" ] || [ -f "$HOME/.hawkeye-backup.env" ]; then
    echo "== backup"
    scripts/backup-offsite.sh
  else
    echo "!! WARNING: no backup settings (~/.hawkeye-host.env or ~/.hawkeye-backup.env), deploying WITHOUT a backup (docs/secrets.md)" >&2
  fi

  if release "$TARGET" && healthy; then
    docker image prune -f >/dev/null
    echo "== deployed ${TARGET:0:12}: smoke test passed"
    update_regions
    # Every release builds here, so old images and build cache fill the disk (25GB of cache after a few weeks). Only after a healthy release
    # (never during a rollback) and after the workers took the running image: remove images no container uses that are over a week old (a
    # rollback rebuilds from the commit, it does not need them), and trim the build cache to its most recently used 5GB. That keeps the Go
    # module and compile caches (the --mount=type=cache lines in the Dockerfiles) that keep a release inside the workflow's time limit, so
    # never `docker builder prune -a` here. A cleanup failure never fails a release that is already live.
    docker image prune -a -f --filter "until=168h" >/dev/null || echo "!! could not remove old images; the release is fine" >&2
    docker builder prune -f --keep-storage 5GB >/dev/null || echo "!! could not trim the build cache; the release is fine" >&2
    # This release changed the deploy script: run the new copy once for the same commit (it finds itself unchanged, so it never repeats).
    if [ "$(sha256sum scripts/deploy.sh | cut -d' ' -f1)" != "$SELF_SUM" ]; then
      echo "== this release changed scripts/deploy.sh: deploying ${TARGET:0:12} again with the new copy"
      exec 9>&-   # release the lock; the new copy takes it again
      exec scripts/deploy.sh "$TARGET"
    fi
    exit 0
  fi

  echo "!! deploy of ${TARGET:0:12} failed; rolling back to ${PREVIOUS:0:12}" >&2
  compose logs --tail 40 api web worker >&2 || true
  cp -p .env.production.previous .env.production   # the settings that were live, with the code that was live
  release "$PREVIOUS" keep
  if healthy; then echo "!! rolled back: ${PREVIOUS:0:12} is live again. The failed release did not go out." >&2
  else echo "!! ROLLBACK ALSO FAILED: the site needs attention (docker compose logs on the VM)" >&2; fi
  exit 1   # workers were not sent the failed release: they update only after a healthy deploy
}
main "$@"
exit $?
