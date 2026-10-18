#!/usr/bin/env bash
# Deploys a commit of this repo on the production host: back up, build, migrate, restart, smoke-test, and roll back if the smoke test fails.
#
#   scripts/deploy.sh [COMMIT_SHA]        # default: the tip of origin/main
#
# Run on the VM as the deploy user (from the repo root) or over SSH by the GitHub Actions workflow (.github/workflows/deploy.yml). When the
# SSH key is restricted with a forced command (docs/deployment.md), the sha arrives in $SSH_ORIGINAL_COMMAND. Exit 0 means the new version
# is live and healthy; non-zero means it was rolled back (or never started) and the log says why.
set -euo pipefail
# Everything lives in one function that is parsed before it runs: the checkout below replaces this very file, and bash must not read a
# half-changed script.
main() {
  cd "$(dirname "$0")/.."
  ROOT="$PWD"

  TARGET="${1:-${SSH_ORIGINAL_COMMAND:-}}"
  if [ -n "$TARGET" ] && ! [[ "$TARGET" =~ ^[0-9a-f]{40}$ ]]; then echo "refusing: '$TARGET' is not a full commit sha" >&2; exit 2; fi

  exec 9>"$ROOT/.deploy.lock"
  flock -n 9 || { echo "another deploy is running" >&2; exit 1; }

  COMPOSE=(docker compose --env-file .env.production -f deploy/docker-compose.prod.yml -f deploy/docker-compose.local-db.yml)
  [ -f .env.production ] || { echo ".env.production is missing" >&2; exit 1; }
  SITE="$(sed -n 's/^SITE_ADDRESS=\([^ #]*\).*/\1/p' .env.production)"
  [ -n "$SITE" ] || { echo "SITE_ADDRESS not found in .env.production" >&2; exit 1; }

  PREVIOUS="$(git rev-parse HEAD)"
  git fetch --quiet origin main
  [ -n "$TARGET" ] || TARGET="$(git rev-parse origin/main)"
  git cat-file -e "$TARGET^{commit}" 2>/dev/null || { echo "commit $TARGET is not in the repository (not pushed?)" >&2; exit 1; }
  echo "== deploying ${TARGET:0:12} (currently ${PREVIOUS:0:12}) to https://$SITE"

  # 1. A backup first: migrations are forward-only, so this is the way back from a bad one. Skipped only if off-host backups are not set up yet.
  if [ -f "$HOME/.hawkeye-backup.env" ]; then
    echo "== backup"
    scripts/backup-offsite.sh
  else
    echo "!! WARNING: ~/.hawkeye-backup.env not found, deploying WITHOUT a backup (set up scripts/backup-offsite.sh)" >&2
  fi

  release() { # sha: check out the code, label the release, build, migrate, start
    local sha="$1"
    git checkout --quiet main && git reset --quiet --hard "$sha"   # stay on the main branch so a manual `git pull` keeps working
    sed -i "s/^APP_VERSION=.*/APP_VERSION=${sha:0:12}/" .env.production   # shown in logs and Sentry; also tags the images
    "${COMPOSE[@]}" up -d --build --remove-orphans
  }
  healthy() { # smoke test against the public URL, retrying while containers settle
    local i
    for i in 1 2 3 4 5 6; do
      if scripts/smoke.sh "https://$SITE" >/tmp/hawkeye-smoke.log 2>&1; then return 0; fi
      sleep 10
    done
    cat /tmp/hawkeye-smoke.log; return 1
  }

  if release "$TARGET" && healthy; then
    docker image prune -f >/dev/null
    echo "== deployed ${TARGET:0:12}: smoke test passed"
    exit 0
  fi

  echo "!! deploy of ${TARGET:0:12} failed; rolling back to ${PREVIOUS:0:12}" >&2
  "${COMPOSE[@]}" logs --tail 40 api web worker >&2 || true
  release "$PREVIOUS"
  if healthy; then echo "!! rolled back: ${PREVIOUS:0:12} is live again. The failed release did not go out." >&2
  else echo "!! ROLLBACK ALSO FAILED: the site needs attention (docker compose logs on the VM)" >&2; fi
  exit 1
}
main "$@"
exit $?
