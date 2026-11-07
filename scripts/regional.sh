#!/usr/bin/env bash
# Regional check workers, from YOUR machine. You rarely run this directly: `scripts/rebuild.sh regions` creates the hosts and runs `link` and `verify`, and
# `scripts/rebuild.sh cutover` runs `link`. Releases and settings are not done here at all: the production server sends them on every deploy
# (scripts/regional-sync.sh). docs/deployment.md, "Regional worker hosts", has the whole picture.
#
#   scripts/regional.sh status          what each host is doing, and which regions customers can choose
#   scripts/regional.sh link            build or refresh the tunnel: every worker in infra/, and no others, become the hub's peers
#   scripts/regional.sh verify [CODE]   check it is healthy and on production's release, and prove its egress firewall with scripts/verify-egress.sh
#
# No secrets pass through this script: only public keys.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"; INFRA="$ROOT/infra"
HUB_TUNNEL_IP=10.66.0.1
KNOWN="${HAWKEYE_REGIONAL_KNOWN_HOSTS:-$INFRA/.known_hosts_regional}"
SSH=(ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o "UserKnownHostsFile=$KNOWN" -o ConnectTimeout=15)

die() { echo "regional: $*" >&2; exit 1; }
on() { local host="$1"; shift; "${SSH[@]}" "deploy@$host" "$@"; }
tf() { (cd "$INFRA" && tofu "$@"); }

load_infra() {
  for t in tofu jq ssh; do command -v "$t" >/dev/null || die "$t is not installed"; done
  [ "$(tf workspace show)" = default ] || die "select the production workspace first: regional workers exist only there"
  HUB="$(tf output -raw public_ip)"
  WORKERS="$(tf output -json workers)"
  [ "$(jq length <<<"$WORKERS")" -gt 0 ] || die "no workers in infra/: list them in infra/regions.json and run scripts/rebuild.sh regions"
}
codes() { # the region given, or all of them
  if [ -n "${1:-}" ]; then
    jq -e --arg c "$1" 'has($c)' <<<"$WORKERS" >/dev/null || die "no worker called $1 in infra/"
    echo "$1"
  else jq -r 'keys[]' <<<"$WORKERS"; fi
}
ip_of() { jq -r --arg c "$1" '.[$c].ip' <<<"$WORKERS"; }
tunnel_of() { jq -r --arg c "$1" '.[$c].tunnel_ip' <<<"$WORKERS"; }

wait_for_first_boot() {
  local i
  for i in $(seq 1 "${REGIONAL_TRIES:-30}"); do
    if on "$1" 'test -f /var/lib/hawkeye-firstboot-done' 2>/dev/null; then return 0; fi
    echo "   waiting for first-boot setup on $1"; sleep "${REGIONAL_WAIT:-10}"
  done
  die "$1 did not finish first-boot setup (see /var/log/hawkeye-cloud-init.log there)"
}

cmd_link() {
  local code ip pub hub_pub release_key peers=()
  echo "== hub $HUB"
  hub_pub="$(on "$HUB" 'sudo bash -s -- hub-key' < deploy/wireguard.sh)"
  # The key the production server sends releases with. It is made there, and only its public half travels.
  release_key="$(on "$HUB" 'test -f ~/.ssh/hawkeye-regional || ssh-keygen -q -t ed25519 -N "" -C hawkeye-regional -f ~/.ssh/hawkeye-regional; cat ~/.ssh/hawkeye-regional.pub')"
  [[ "$release_key" =~ ^ssh-ed25519\ [A-Za-z0-9+/=]+\ hawkeye-regional$ ]] || die "unexpected release key from the hub: $release_key"
  for code in $(codes); do
    ip="$(ip_of "$code")"
    wait_for_first_boot "$ip"
    pub="$(on "$ip" 'sudo bash -s -- worker-key' < deploy/wireguard.sh)"
    peers+=("$pub=$(tunnel_of "$code")")
  done
  on "$HUB" "sudo bash -s -- hub-apply ${peers[*]}" < deploy/wireguard.sh
  for code in $(codes); do
    ip="$(ip_of "$code")"
    echo "== $code $ip"
    on "$ip" "sudo bash -s -- worker-apply $hub_pub $HUB $(tunnel_of "$code")" < deploy/wireguard.sh
    # Only from the hub's tunnel address, and only the release receiver. Replaces any earlier key, so a replaced hub takes over.
    printf 'from="%s",restrict,command="/home/deploy/hawkeye/scripts/regional-receive.sh" %s\n' "$HUB_TUNNEL_IP" "$release_key" |
      on "$ip" 'sed -i "/ hawkeye-regional$/d" ~/.ssh/authorized_keys && cat >> ~/.ssh/authorized_keys'
    on "$ip" "ping -c 2 -W 5 $HUB_TUNNEL_IP >/dev/null" || die "$code cannot reach the hub through the tunnel: check the UDP rule in infra/workers.tf and 'sudo wg show' on both ends"
    echo "   tunnel up"
  done
}

cmd_verify() {
  local code ip i state version live
  live="$(on "$HUB" "sed -n 's/^APP_VERSION=//p' hawkeye/.env.production" | tail -1)"
  for code in $(codes "${1:-}"); do
    ip="$(ip_of "$code")"
    echo "== $code"
    state=""
    for i in $(seq 1 "${REGIONAL_TRIES:-12}"); do
      state="$(on "$ip" "docker inspect -f '{{.State.Health.Status}}' hawkeye-worker-1" 2>/dev/null || true)"
      [ "$state" = healthy ] && break
      sleep "${REGIONAL_WAIT:-5}"
    done
    [ "$state" = healthy ] || die "$code is not healthy (state: ${state:-not running}). Its health check needs the database, so look at the tunnel first"
    echo "   healthy: it can reach the database"
    version="$(on "$ip" "sed -n 's/^APP_VERSION=//p' hawkeye/.env.worker" | tail -1)"
    [ "$version" = "$live" ] || die "$code is on $version, production is on $live"
    echo "   on production's release ($version)"
    on "$ip" 'cd hawkeye && scripts/verify-egress.sh'
  done
}

cmd_status() {
  local code ip live
  live="$(on "$HUB" "sed -n 's/^CHECK_REGIONS=//p' hawkeye/.env.production" | tail -1 | tr -d "\"'")"
  for code in $(codes); do
    ip="$(ip_of "$code")"
    printf '%-14s %-16s tunnel %-10s worker %-10s version %s\n' "$code" "$ip" \
      "$(on "$ip" "sudo wg show wg0 latest-handshakes 2>/dev/null | awk -v now=\$(date +%s) '{print (\$2 > 0 && now - \$2 < 180) ? \"up\" : \"down\"}'" 2>/dev/null || echo unreachable)" \
      "$(on "$ip" "docker inspect -f '{{.State.Health.Status}}' hawkeye-worker-1" 2>/dev/null || echo stopped)" \
      "$(on "$ip" "sed -n 's/^APP_VERSION=//p' hawkeye/.env.worker" 2>/dev/null || echo none)"
  done
  if [ -n "$live" ]; then echo "Customers can choose: $live"
  else echo "No region is launched: the workers are idle. To launch one, set \"launched\": true in infra/regions.json and merge."; fi
}

CMD="${1:-}"; [ $# -gt 0 ] && shift
case "$CMD" in
  status|link) load_infra; "cmd_$CMD" ;;
  verify) load_infra; cmd_verify "${1:-}" ;;
  *) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; [ -z "$CMD" ] && exit 0; exit 2 ;;
esac
