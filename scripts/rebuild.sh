#!/usr/bin/env bash
# Build, test and cut over to a server created from infra/ (OpenTofu/Terraform), step by step, with checks between the steps.
# Runs on YOUR machine (it needs your secrets, your age key and your cloud tokens). Read docs/rebuild.md first.
#
#   scripts/rebuild.sh preflight                      check tools, tokens, secrets, backups; changes nothing
#   scripts/rebuild.sh plan                           preflight + show what OpenTofu would change
#   scripts/rebuild.sh create  [--workspace NAME]     create the server and DNS name(s); waits until first-boot setup is done
#   scripts/rebuild.sh stage   [--workspace NAME]     restore the newest backup onto it and start it on its own test name; smoke-tests it
#   scripts/rebuild.sh cutover                        THE switch: freeze the old server, final backup, restore, move DNS, start, verify
#   scripts/rebuild.sh rollback                       point DNS back at the old server and restart it
#   scripts/rebuild.sh destroy --workspace NAME       delete a rehearsal server (never the production one)
#   scripts/rebuild.sh credentials --host IP          give an EXISTING server the secret manager credentials (one time; new servers get them in `create`)
#
# Settings come from the secret manager (Infisical) when ~/.hawkeye-secrets/.hawkeye-infisical.env exists (docs/secrets.md): that one small file is
# all a new server is given, and it fetches the rest itself. Without it this script falls back to the older files in ~/.hawkeye-secrets.
#
# Options: --workspace NAME (default "default" = production; use a name like "drill" to rehearse), --dry-run (print what would change), --yes (skip the
# "type this to continue" prompts; never skips the cutover prompt).
#
# A rehearsal ("drill") never touches the real site's DNS and never starts the worker, so it cannot send alerts to your customers.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"; INFRA="$ROOT/infra"
SECRETS="${HAWKEYE_SECRETS_DIR:-$HOME/.hawkeye-secrets}"
AGE_KEY="${AGE_KEY_FILE:-$HOME/hawkeye-backup-key.txt}"
DEPLOY_PUB="${DEPLOY_PUBKEY_FILE:-$HOME/hawkeye-deploy-key.pub}"

CMD="${1:-}"; [ $# -gt 0 ] && shift
WS="default"; DRY=0; YES=0; HOST_ARG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --workspace) WS="${2:?--workspace needs a name}"; shift ;;
    --dry-run) DRY=1 ;;
    --yes) YES=1 ;;
    --host) HOST_ARG="${2:?--host needs an address}"; shift ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac; shift
done

WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT   # scratch space (it briefly holds the decrypted backup); deleted when the script ends
say()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m   %s\n' "$*"; }
warn() { printf '  \033[33mwarn\033[0m %s\n' "$*"; }
die()  { printf '\n\033[31mSTOP: %s\033[0m\n' "$*" >&2; exit 1; }
run()  { if [ "$DRY" = 1 ]; then printf '  [dry-run] %s\n' "$*"; else "$@"; fi; }
confirm() { # confirm PHRASE: the user must type it exactly
  [ "$YES" = 1 ] && [ "${2:-}" != "always" ] && return 0
  [ "$DRY" = 1 ] && { echo "  [dry-run] would ask you to type: $1"; return 0; }
  printf '\nType  %s  to continue (anything else stops): ' "$1"; read -r answer
  [ "$answer" = "$1" ] || die "not confirmed, nothing more was done"
}

# ---- settings -------------------------------------------------------------------------------------------------------------------------------
export CLOUDFLARE_API_TOKEN="${CLOUDFLARE_API_TOKEN:-${CF_DNS_API_TOKEN:-}}"
export TF_VAR_cloudflare_zone_id="${TF_VAR_cloudflare_zone_id:-${CF_ZONE_ID:-}}"
ZONE="$TF_VAR_cloudflare_zone_id"
ENVFILE="$SECRETS/.env.production"                        # the older way: files kept here and copied to the server
SMFILE="$SECRETS/.hawkeye-infisical.env"                   # the new way: credentials for the secret manager (docs/secrets.md)
USE_SM=0; [ -f "$SMFILE" ] && USE_SM=1
sm() { HAWKEYE_INFISICAL_ENV="$SMFILE" python3 "$ROOT/scripts/fetch-secrets.py" "$@"; }
# The few settings this script needs itself (the site, the database user and name). They are read from a private file that exists only for the
# moment it takes to read them: no value is ever printed.
APPVALS="$WORK/app.env"
if [ "$USE_SM" = 1 ]; then (umask 077; sm write --app-out "$APPVALS" >/dev/null 2>&1) || rm -f "$APPVALS"; else APPVALS="$ENVFILE"; fi
site_value() { [ -f "$APPVALS" ] && sed -n "s/^$1=\([^ #]*\).*/\1/p" "$APPVALS" | head -1 || true; }
SITE="$(site_value SITE_ADDRESS)"
PGU="$(site_value POSTGRES_USER)"; PGU="${PGU:-hawkeye}"; PGDB="$(site_value POSTGRES_DB)"; PGDB="${PGDB:-monitoring}"
[ "$USE_SM" = 1 ] && rm -f "$APPVALS"
COMPOSE='docker compose --env-file .env.production -f deploy/docker-compose.prod.yml -f deploy/docker-compose.local-db.yml'
KH="$INFRA/.known_hosts_$WS"
LIVE="$INFRA/live.auto.tfvars"

tf()  { (cd "$INFRA" && tofu "$@"); }
cf()  { curl -sS -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" "https://api.cloudflare.com/client/v4/$1"; }
ssh_opts() { echo "-o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=accept-new -o HashKnownHosts=no -o UserKnownHostsFile=$1"; }
# shellcheck disable=SC2086
rssh()  { local host="$1" kh="$2"; shift 2; ssh $(ssh_opts "$kh") "deploy@$host" "$@"; }
# shellcheck disable=SC2086
rscp()  { local kh="$1"; shift; scp -q $(ssh_opts "$kh") "$@"; }
ws_select() { [ "$DRY" = 1 ] || tf workspace select -or-create "$WS" >/dev/null; }
new_ip()    { tf output -raw public_ip 2>/dev/null; }
stage_host() { tf output -raw stage_host 2>/dev/null; }
live_get()  { sed -n "s/^$1 *= *\"\{0,1\}\([^\"]*\)\"\{0,1\}$/\1/p" "$LIVE" 2>/dev/null | head -1; }

# The backup bucket's settings (BACKUP_*, AGE_RECIPIENT) into this shell, and rclone configured from them. Never printed or kept: from the secret
# manager they pass through a private temporary file that is deleted at once.
load_backup_settings() {
  local f="$WORK/backup.env"
  if [ "$USE_SM" = 1 ]; then (umask 077; sm host-env --out "$f" >/dev/null) || die "could not read the backup settings from the secret manager"
  else f="$SECRETS/.hawkeye-backup.env"; fi
  # shellcheck disable=SC1090
  set -a; . "$f"; set +a
  [ "$USE_SM" = 1 ] && rm -f "$f"
  export RCLONE_CONFIG_OFFSITE_TYPE=s3 RCLONE_CONFIG_OFFSITE_PROVIDER=Other RCLONE_CONFIG_OFFSITE_ENDPOINT="https://$BACKUP_ENDPOINT"
  export RCLONE_CONFIG_OFFSITE_ACCESS_KEY_ID="$BACKUP_ACCESS_KEY" RCLONE_CONFIG_OFFSITE_SECRET_ACCESS_KEY="$BACKUP_SECRET_KEY"
}

# Give the server the one file it needs to fetch everything else. It is the only secret this script ever copies to a server.
deliver_credentials() { # IP
  [ "$USE_SM" = 1 ] || return 0
  run rscp "$KH" "$SMFILE" "deploy@$1:.hawkeye-infisical.env"
  run rssh "$1" "$KH" 'chmod 600 ~/.hawkeye-infisical.env'
  [ "$DRY" = 1 ] || ok "secret manager credentials delivered to $1"
}

# The newest encrypted dump in the bucket, downloaded, checked and decrypted into $1 as $1/restore.dump. Your age key never leaves this machine.
fetch_latest_dump() {
  local dir="$1" newer_than="${2:-}"
  load_backup_settings
  local dest="offsite:$BACKUP_BUCKET/db" latest
  latest="$(rclone lsf "$dest/" --include 'hawkeye-*.dump.age' | sort | tail -1)"
  [ -n "$latest" ] || die "no backups found in $BACKUP_BUCKET/db/"
  if [ -n "$newer_than" ]; then
    local stamp="${latest#hawkeye-}"; stamp="${stamp%%.dump.age}"
    [[ "$stamp" > "$newer_than" ]] || die "the newest backup ($stamp) is not newer than the freeze ($newer_than): the final backup did not land"
  fi
  ok "newest backup: $latest"
  rclone copy "$dest/$latest" "$dir/" --checksum; rclone copy "$dest/$latest.sha256" "$dir/" --checksum
  (cd "$dir" && shasum -a 256 -c "$latest.sha256" >/dev/null) || die "checksum mismatch on $latest: the backup is damaged"
  age -d -i "$AGE_KEY" -o "$dir/restore.dump" "$dir/$latest" || die "could not decrypt $latest with $AGE_KEY"
  ok "checksum verified and decrypted"
  FETCHED="$latest"
}

# Restore $1/restore.dump into the Postgres container on the new server. $2 = "reset" drops and recreates the (stage) database first.
restore_on_server() {
  local ip="$1" dir="$2" mode="${3:-keep}" tables
  run rssh "$ip" "$KH" "cd ~/hawkeye && $COMPOSE up -d db >/dev/null && until docker exec hawkeye-db-1 pg_isready -U $PGU -d $PGDB >/dev/null 2>&1; do sleep 2; done"
  if [ "$mode" = reset ]; then
    run rssh "$ip" "$KH" "docker exec hawkeye-db-1 psql -U $PGU -d postgres -qtAc \"DROP DATABASE IF EXISTS \\\"$PGDB\\\" WITH (FORCE)\" && docker exec hawkeye-db-1 psql -U $PGU -d postgres -qtAc \"CREATE DATABASE \\\"$PGDB\\\"\""
  fi
  tables="$([ "$DRY" = 1 ] && echo 0 || rssh "$ip" "$KH" "docker exec hawkeye-db-1 psql -U $PGU -d $PGDB -qtAc \"select count(*) from information_schema.tables where table_schema='public'\"")"
  if [ "$tables" != 0 ]; then warn "the database on the new server already has $tables tables: not restoring over it"; return 0; fi
  run rscp "$KH" "$dir/restore.dump" "deploy@$ip:restore.dump"
  run rssh "$ip" "$KH" "cd ~/hawkeye && scripts/restore.sh ~/restore.dump --docker hawkeye-db-1 --user $PGU --db $PGDB; rm -f ~/restore.dump"
  ok "database restored"
}

wait_for_server() {
  local ip="$1" i
  say "Waiting for first-boot setup on $ip (a few minutes)"
  for i in $(seq 1 90); do
    if rssh "$ip" "$KH" 'test -f /var/lib/hawkeye-firstboot-done' 2>/dev/null; then ok "first-boot setup is finished"; return 0; fi
    # If a deploy login still does not work after ~6 minutes, the setup file was not applied: say why instead of waiting out the clock.
    if [ "$i" = 36 ]; then diagnose_server "$ip"; fi
    sleep 10
  done
  diagnose_server "$ip"
}

diagnose_server() { # root still has the SSH key Vultr installed, so we can look inside even when cloud-init did not create the deploy user
  local ip="$1"
  echo; warn "first-boot setup has not finished. What the server says (read-only):"
  # shellcheck disable=SC2086
  ssh $(ssh_opts "$KH") "root@$ip" '
    echo "-- cloud-init status:"; cloud-init status --long 2>&1 | head -8
    echo "-- deploy user:"; id deploy 2>&1
    echo "-- cloud-init warnings:"; grep -E "WARNING|ERROR|Traceback" /var/log/cloud-init.log 2>/dev/null | tail -6 | cut -c1-200
    echo "-- first-boot log:"; tail -12 /var/log/hawkeye-cloud-init.log 2>&1' 2>&1 || warn "could not even log in as root"
  die "the server was not set up by cloud-init (see above). Rehearsals can simply be destroyed and recreated: scripts/rebuild.sh destroy --workspace $WS"
}

smoke_url() { # wait for the site to answer with a valid certificate, then run the full smoke test
  local url="$1" i
  for i in $(seq 1 40); do curl -fsS -m 8 -o /dev/null "$url/readyz" 2>/dev/null && break; sleep 6; done
  curl -fsS -m 8 -o /dev/null "$url/readyz" || die "$url/readyz never answered (certificate not issued yet, or the stack failed: see docker compose logs on the server)"
  scripts/smoke.sh "$url" || die "the smoke test failed against $url"
}

# ---- commands -------------------------------------------------------------------------------------------------------------------------------
cmd_preflight() {
  say "Preflight (read-only)"
  local t; for t in tofu rclone age gh jq ssh scp curl shasum; do command -v "$t" >/dev/null && ok "$t installed" || die "$t is not installed"; done
  local v; for v in VULTR_API_KEY CLOUDFLARE_API_TOKEN TF_VAR_cloudflare_zone_id; do [ -n "${!v:-}" ] && ok "$v is set" || die "$v is not set (Cloudflare: CF_DNS_API_TOKEN and CF_ZONE_ID are mapped automatically)"; done
  if [ "$USE_SM" = 1 ]; then
    sm check > "$WORK/sm-check" 2>&1 && ok "secret manager reachable; $(head -1 "$WORK/sm-check")" || { cat "$WORK/sm-check" >&2; die "the secret manager's settings are not usable (above)"; }
  else
    for v in .env.production .hawkeye-backup.env; do [ -f "$SECRETS/$v" ] && ok "$SECRETS/$v exists" || die "missing $SECRETS/$v (docs/secrets.md: set up the secret manager, or docs/rebuild.md for the older files)"; done
    ! grep -q CHANGE_ME "$ENVFILE" && ok ".env.production has no placeholders" || die ".env.production still contains CHANGE_ME"
    warn "settings are copied from files on this machine; the secret manager removes that step and the drift between copies (docs/secrets.md)"
  fi
  [ -f "$AGE_KEY" ] && ok "age key found" || die "age private key not found at $AGE_KEY"
  [ -f "$DEPLOY_PUB" ] && ok "deploy public key found" || die "deploy public key not found at $DEPLOY_PUB"
  [ -n "$SITE" ] && ok "site address: $SITE" || die "SITE_ADDRESS is not available (secret manager or $ENVFILE)"
  [ "$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $VULTR_API_KEY" https://api.vultr.com/v2/account)" = 200 ] && ok "Vultr API reachable with your key" || die "Vultr API rejected the key (is this machine's IP allowed under Account > API?)"
  [ "$(cf "zones/$ZONE" | jq -r '.success')" = true ] && ok "Cloudflare zone readable" || die "Cloudflare rejected the token or zone id"
  [ "$(cf "zones/$ZONE" | jq -r '.result.name')" = "$SITE" ] || die "the Cloudflare zone is not $SITE"
  git fetch -q origin main 2>/dev/null || true
  [ -z "$(git rev-list origin/main..HEAD 2>/dev/null)" ] && ok "everything is pushed (the new server clones main)" || die "local commits are not pushed: the server clones main from GitHub, so push first"
  git ls-tree -r origin/main --name-only | grep -q '^scripts/deploy.sh$' && ok "origin/main has the deploy scripts" || die "origin/main does not contain scripts/deploy.sh"
  [ "$USE_SM" = 0 ] || { git ls-tree -r origin/main --name-only | grep -q '^scripts/sync-secrets.sh$' && ok "origin/main has scripts/sync-secrets.sh" || die "origin/main does not contain scripts/sync-secrets.sh: push it first, the new server clones main"; }
  [ -z "$(git status --porcelain -- scripts deploy docker-compose* 2>/dev/null)" ] && ok "scripts/ and deploy/ are committed" || warn "uncommitted changes in scripts/ or deploy/: the new server clones main from GitHub, so it will not have them until they are committed and pushed"
  tf init -input=false >/dev/null && tf validate >/dev/null && ok "infra/ validates"
  local tmp="$WORK/preflight"; mkdir -p "$tmp"
  ( load_backup_settings
    n="$(rclone lsf "offsite:$BACKUP_BUCKET/db/" --include 'hawkeye-*.dump.age' | wc -l | tr -d ' ')"; echo "$n" > "$tmp/n" ) 2>/dev/null || die "cannot read the backup bucket (check the BACKUP_* settings in the secret manager, or ~/.hawkeye-secrets/.hawkeye-backup.env)"
  [ "$(cat "$tmp/n")" -gt 0 ] && ok "$(cat "$tmp/n") backup(s) in the bucket" || die "the bucket has no backups yet; run scripts/backup-offsite.sh on the live server first"
  echo; echo "Preflight passed."
}

cmd_plan() { cmd_preflight; say "Plan (workspace: $WS)"; ws_select; prepare_live; tf plan -input=false; }

prepare_live() { # remember where the site points today, and the deploy key, in a git-ignored file (rehearsals get the same deploy key, so it is tested too)
  if [ ! -f "$LIVE" ]; then
    local old; old="$(cf "zones/$ZONE/dns_records?type=A&name=$SITE" | jq -r '.result[0].content')"
    [ -n "$old" ] && [ "$old" != null ] || die "no A record for $SITE found in Cloudflare"
    say "Recording the current A record ($SITE -> $old) in infra/live.auto.tfvars"
    local line="restrict,command=\"cd /home/deploy/hawkeye && scripts/deploy.sh\" $(cat "$DEPLOY_PUB")"
    if [ "$DRY" = 1 ]; then echo "  [dry-run] would write cutover=false, old_ip=$old, deploy_authorized_key"; DRYVARS="-var old_ip=$old -var cutover=false"; return 0; fi
    { echo "cutover = false"; echo "old_ip = \"$old\""; echo "deploy_authorized_key = \"${line//\"/\\\"}\""; } > "$LIVE"
  fi
}

import_dns() { # adopt the existing apex A and www records instead of creating duplicates
  [ "$WS" = default ] || return 0
  local addr name type id
  for pair in 'cloudflare_dns_record.apex[0]|A|'"$SITE" 'cloudflare_dns_record.www[0]|CNAME|www.'"$SITE"; do
    addr="${pair%%|*}"; rest="${pair#*|}"; type="${rest%%|*}"; name="${rest#*|}"
    if [ "$DRY" = 1 ]; then echo "  [dry-run] would import the existing $type record $name as $addr if it is not in state"; continue; fi
    if tf state list 2>/dev/null | grep -qF "$addr"; then ok "$addr already managed"; continue; fi
    id="$(cf "zones/$ZONE/dns_records?type=$type&name=$name" | jq -r '.result[0].id')"
    [ -n "$id" ] && [ "$id" != null ] || { warn "no existing $type record for $name: it will be created"; continue; }
    tf import -input=false "$addr" "$ZONE/$id" >/dev/null && ok "imported $name"
  done
}

cmd_create() {
  cmd_preflight; say "Create (workspace: $WS)"
  ws_select; prepare_live; import_dns
  # shellcheck disable=SC2086
  [ "$DRY" = 1 ] && { tf plan -input=false ${DRYVARS:-} || true; echo; echo "  [dry-run] stopping before apply"; return 0; }
  tf plan -input=false -out=.plan
  [ "$WS" = default ] && echo "  (This creates a NEW server next to the old one and a 'staging' DNS name. The live site's A record is not changed.)"
  confirm "create $WS"
  tf apply -input=false .plan; rm -f "$INFRA/.plan"
  rm -f "$KH"
  local ip; ip="$(new_ip)"; ok "server IP: $ip"
  wait_for_server "$ip"
  deliver_credentials "$ip"
  echo; echo "Next: scripts/rebuild.sh stage$([ "$WS" = default ] || echo " --workspace $WS")"
}

cmd_stage() {
  ws_select; local ip host tmp; ip="$(new_ip)"; host="$(stage_host)"
  [ -n "$ip" ] || die "no server in workspace $WS yet: run create first"
  say "Stage $host ($ip): restore the newest backup and start it WITHOUT the worker"
  wait_for_server "$ip"
  tmp="$WORK/stage"; mkdir -p "$tmp"
  if [ "$USE_SM" = 1 ]; then
    # The server fetches the production settings itself, with the stage's own address in place of the real one.
    deliver_credentials "$ip"
    run rssh "$ip" "$KH" "cd ~/hawkeye && scripts/sync-secrets.sh --override SITE_ADDRESS=$host --override APP_BASE_URL=https://$host --override API_BASE_URL=https://$host --drop WWW_ADDRESS"
  else
    sed -e "s|^SITE_ADDRESS=.*|SITE_ADDRESS=$host|" -e "s|^APP_BASE_URL=.*|APP_BASE_URL=https://$host|" -e "s|^API_BASE_URL=.*|API_BASE_URL=https://$host|" -e '/^WWW_ADDRESS=/d' "$ENVFILE" > "$tmp/env"
    run rscp "$KH" "$tmp/env" "deploy@$ip:hawkeye/.env.production"
    run rssh "$ip" "$KH" 'chmod 600 ~/hawkeye/.env.production'
  fi
  fetch_latest_dump "$tmp"
  restore_on_server "$ip" "$tmp" keep
  # No worker here: a copy of production must never run checks or send alerts to your customers.
  run rssh "$ip" "$KH" "cd ~/hawkeye && $COMPOSE up -d --build migrate api web"
  [ "$DRY" = 1 ] || smoke_url "https://$host"
  say "Stage is healthy at https://$host"
  [ "$WS" = default ] && echo "Look around it if you like (log in with your real account). When ready: scripts/rebuild.sh cutover"
  [ "$WS" = default ] || echo "Rehearsal passed. Clean up with: scripts/rebuild.sh destroy --workspace $WS"
}

cmd_cutover() {
  [ "$WS" = default ] || die "cutover is for production only"
  ws_select; local ip old host started tmp frozen
  ip="$(new_ip)"; host="$(stage_host)"; old="$(live_get old_ip)"
  [ -n "$ip" ] && [ -n "$old" ] || die "run create and stage first"
  [ "$(live_get cutover)" = true ] && die "the cutover is already done (rollback undoes it)"
  say "Pre-checks"
  curl -fsS -m 8 -o /dev/null "https://$host/readyz" && ok "stage answers at https://$host" || die "stage is not answering: run stage first"
  rssh "$old" "$INFRA/.known_hosts_old" 'true' && ok "can reach the old server ($old)" || die "cannot ssh to the old server deploy@$old"
  cat <<EOF

THIS IS THE REAL SWITCH. What happens, in order:
  1. The OLD server ($old) stops its web, api and worker (a short outage starts; checks pause and catch up afterwards).
  2. It takes a final backup, which is restored onto the NEW server ($ip), replacing the staged copy.
  3. DNS for $SITE moves to the new server (TTL 60 s), the full stack starts there (certificate is issued on first request).
  4. The site is smoke-tested at https://$SITE, and GitHub's DEPLOY_HOST / DEPLOY_KNOWN_HOSTS secrets are updated.
The old server is left stopped, not deleted, so 'rollback' can bring it back. Expect roughly 5 to 15 minutes of downtime.
EOF
  confirm "cutover $SITE" always
  tmp="$WORK/cutover"; mkdir -p "$tmp"
  say "1/4 Freeze the old server and take the final backup"
  frozen="$(date -u +%Y%m%dT%H%M%SZ)"
  run rssh "$old" "$INFRA/.known_hosts_old" "cd ~/hawkeye && $COMPOSE stop web api worker"
  run rssh "$old" "$INFRA/.known_hosts_old" "cd ~/hawkeye && scripts/backup-offsite.sh"
  say "2/4 Restore it on the new server"
  [ "$DRY" = 1 ] && { echo "  [dry-run] would fetch the newest backup (must be newer than $frozen), reset the new database and restore it"; } || {
    fetch_latest_dump "$tmp" "$frozen"
    run rssh "$ip" "$KH" "cd ~/hawkeye && $COMPOSE stop web api migrate >/dev/null 2>&1 || true"
    restore_on_server "$ip" "$tmp" reset
  }
  if [ "$USE_SM" = 1 ]; then
    # The real settings (the stage's own address is replaced by the production one), fetched by the server itself.
    run rssh "$ip" "$KH" "cd ~/hawkeye && scripts/sync-secrets.sh"
  else
    run rscp "$KH" "$ENVFILE" "deploy@$ip:hawkeye/.env.production"
    run rscp "$KH" "$SECRETS/.hawkeye-backup.env" "deploy@$ip:.hawkeye-backup.env"
    [ -f "$SECRETS/.hawkeye-heartbeat.env" ] && run rscp "$KH" "$SECRETS/.hawkeye-heartbeat.env" "deploy@$ip:.hawkeye-heartbeat.env"
    run rssh "$ip" "$KH" 'chmod 600 ~/hawkeye/.env.production ~/.hawkeye-backup.env ~/.hawkeye-heartbeat.env 2>/dev/null || true'
  fi
  say "3/4 Move DNS and start everything"
  if [ "$DRY" = 1 ]; then echo "  [dry-run] would set cutover = true in infra/live.auto.tfvars and apply"; else
    sed -i.bak 's/^cutover *=.*/cutover = true/' "$LIVE" && rm -f "$LIVE.bak"
    tf apply -input=false -auto-approve >/dev/null && ok "DNS now points $SITE at $ip (TTL 60 s)"
  fi
  run rssh "$ip" "$KH" "cd ~/hawkeye && $COMPOSE up -d --build --remove-orphans"
  say "4/4 Verify"
  if [ "$DRY" = 1 ]; then echo "  [dry-run] would wait for https://$SITE, run the smoke test and set the GitHub secrets"; return 0; fi
  smoke_url "https://$SITE" || { echo "If this cannot be fixed quickly: scripts/rebuild.sh rollback"; exit 1; }
  set_github_secrets "$ip"
  rssh "$ip" "$KH" "cd ~/hawkeye && sudo ufw status | head -1; test -x scripts/verify-egress.sh && echo 'run scripts/verify-egress.sh on the server to prove the egress rules'" || true
  say "Cutover complete: $SITE is served by $ip"
  echo "The old server ($old) is stopped, not deleted. Delete it in Vultr once the new one has run cleanly for a few days."
}

set_github_secrets() {
  local ip="$1" repo line
  repo="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
  line="$(grep ssh-ed25519 "$KH" | head -1)"; [ -n "$line" ] || { warn "no ed25519 host key recorded; set DEPLOY_KNOWN_HOSTS by hand"; return 0; }
  gh secret set DEPLOY_HOST --env production --repo "$repo" --body "$ip" >/dev/null && ok "DEPLOY_HOST updated"
  gh secret set DEPLOY_KNOWN_HOSTS --env production --repo "$repo" --body "$line" >/dev/null && ok "DEPLOY_KNOWN_HOSTS updated"
}

cmd_rollback() {
  [ "$WS" = default ] || die "rollback is for production only"
  ws_select; local old; old="$(live_get old_ip)"; [ -n "$old" ] || die "no old server recorded"
  cat <<EOF

Rollback points $SITE back at the old server ($old) and restarts its stack.
Anything customers did on the NEW server since the cutover (new monitors, sign-ups, check results) is NOT carried back.
EOF
  confirm "rollback $SITE" always
  if [ "$DRY" = 1 ]; then echo "  [dry-run] would set cutover = false, apply, and restart the old stack"; return 0; fi
  sed -i.bak 's/^cutover *=.*/cutover = false/' "$LIVE" && rm -f "$LIVE.bak"
  tf apply -input=false -auto-approve >/dev/null && ok "DNS points at $old again"
  rssh "$old" "$INFRA/.known_hosts_old" "cd ~/hawkeye && $COMPOSE up -d"
  smoke_url "https://$SITE"
  warn "GitHub's DEPLOY_HOST still points at the new server; set it back to $old if deploys should go to the old one"
}

cmd_credentials() { # give an existing server the secret manager credentials, and check that it can use them
  [ "$USE_SM" = 1 ] || die "no credentials file at $SMFILE (docs/secrets.md, step 'one-time setup')"
  [ -n "$HOST_ARG" ] || die "which server? scripts/rebuild.sh credentials --host <ip>"
  KH="$INFRA/.known_hosts_old"
  say "Check the credentials from this machine first"
  sm check || die "the credentials do not work from here either: fix them before giving them to a server"
  say "Give $HOST_ARG the credentials and check what it would fetch"
  deliver_credentials "$HOST_ARG"
  run rssh "$HOST_ARG" "$KH" 'cd ~/hawkeye && test -x scripts/sync-secrets.sh || { echo "this server does not have scripts/sync-secrets.sh yet: deploy the new code first (the next release brings it)"; exit 1; }; scripts/sync-secrets.sh --check'
  echo; echo "Done. Nothing was changed on the server's settings. The next release (or: scripts/deploy.sh secrets-only) will use the secret manager."
}

cmd_destroy() {
  [ "$WS" != default ] || die "refusing to destroy production. Rehearsal workspaces only; for production use OpenTofu directly, by hand, on purpose."
  ws_select; say "Destroy rehearsal workspace '$WS'"; tf state list 2>/dev/null | head -20
  confirm "destroy $WS"
  run tf destroy -input=false -auto-approve
  run tf workspace select default >/dev/null; run tf workspace delete "$WS"
}

case "$CMD" in
  preflight) cmd_preflight ;;
  plan)      cmd_plan ;;
  create)    cmd_create ;;
  stage)     cmd_stage ;;
  cutover)   cmd_cutover ;;
  rollback)  cmd_rollback ;;
  destroy)   cmd_destroy ;;
  credentials) cmd_credentials ;;
  *) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
