#!/usr/bin/env bash
# Deploys a commit of this repo on the production host: back up, build, migrate, restart, smoke-test, and roll back if the smoke test fails.
#
#   scripts/deploy.sh [COMMIT_SHA]        # default: the tip of origin/main
#   scripts/deploy.sh secrets-only        # no new code: refresh the settings from the secret manager and restart what changed
#
# Run on the VM as the deploy user (from the repo root) or over SSH by the GitHub Actions workflow (.github/workflows/deploy.yml). When the
# SSH key is restricted with a forced command (docs/deployment.md), the sha (or "secrets-only") arrives in $SSH_ORIGINAL_COMMAND. Exit 0 means
# the new version is live and healthy; non-zero means it was rolled back (or never started) and the log says why.
#
# Settings: when this machine has credentials for the secret manager (~/.hawkeye-infisical.env), every release first refreshes .env.production
# and ~/.hawkeye-host.env from it (scripts/sync-secrets.sh; docs/secrets.md). If it cannot be reached, the release carries on with the settings
# already on the server and says so. Without credentials, the existing .env.production is used as it is.
set -euo pipefail
# Everything lives in one function that is parsed before it runs: the checkout below replaces this very file, and bash must not read a
# half-changed script.
main() {
  cd "$(dirname "$0")/.."
  ROOT="$PWD"

  TARGET="${1:-${SSH_ORIGINAL_COMMAND:-}}"
  if [ -n "$TARGET" ] && [ "$TARGET" != "secrets-only" ] && ! [[ "$TARGET" =~ ^[0-9a-f]{40}$ ]]; then echo "refusing: '$TARGET' is not a full commit sha (or secrets-only)" >&2; exit 2; fi

  exec 9>"$ROOT/.deploy.lock"
  flock -n 9 || { echo "another deploy is running" >&2; exit 1; }

  COMPOSE=(docker compose --env-file .env.production -f deploy/docker-compose.prod.yml -f deploy/docker-compose.local-db.yml)
  [ -f .env.production ] || { echo ".env.production is missing" >&2; exit 1; }
  SITE="$(sed -n 's/^SITE_ADDRESS=\([^ #]*\).*/\1/p' .env.production)"
  [ -n "$SITE" ] || { echo "SITE_ADDRESS not found in .env.production" >&2; exit 1; }

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
    sed -i '/^APP_VERSION=/d' .env.production; echo "APP_VERSION=${sha:0:12}" >> .env.production   # shown in logs and Sentry; also tags the images
    "${COMPOSE[@]}" up -d --build --remove-orphans
  }

  # The settings as they are now: what a rollback puts back (a new release may bring new settings, and they are part of what failed).
  cp -p .env.production .env.production.previous

  if [ "$TARGET" = "secrets-only" ]; then
    echo "== refreshing settings from the secret manager (no new code)"
    rc=0; scripts/sync-secrets.sh || rc=$?
    if [ "$rc" -ne 0 ]; then echo "!! settings were not refreshed (exit $rc): nothing was changed" >&2; exit 1; fi
    # The header comment carries the time it was written, so compare the settings themselves.
    if diff -q <(grep -v '^#' .env.production) <(grep -v '^#' .env.production.previous) >/dev/null; then echo "== the settings have not changed: nothing to restart"; exit 0; fi
    "${COMPOSE[@]}" up -d --remove-orphans
    if healthy; then echo "== settings applied: smoke test passed"; exit 0; fi
    echo "!! the site is unhealthy with the new settings; putting the previous ones back" >&2
    cp -p .env.production.previous .env.production
    "${COMPOSE[@]}" up -d --remove-orphans
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
    exit 0
  fi

  echo "!! deploy of ${TARGET:0:12} failed; rolling back to ${PREVIOUS:0:12}" >&2
  "${COMPOSE[@]}" logs --tail 40 api web worker >&2 || true
  cp -p .env.production.previous .env.production   # the settings that were live, with the code that was live
  release "$PREVIOUS" keep
  if healthy; then echo "!! rolled back: ${PREVIOUS:0:12} is live again. The failed release did not go out." >&2
  else echo "!! ROLLBACK ALSO FAILED: the site needs attention (docker compose logs on the VM)" >&2; fi
  exit 1
}
main "$@"
exit $?
