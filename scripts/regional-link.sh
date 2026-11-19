#!/usr/bin/env bash
# Joins the regional workers to the tunnel, from the production server (the hub). Nobody runs this by hand: the Infrastructure workflow runs it
# after every apply that leaves regional workers, through the deploy key (`scripts/deploy.sh regions ...`), and every deploy runs `--key` from
# scripts/deploy.sh. docs/deployment.md, "Regional worker hosts", has the whole picture.
#
#   scripts/regional-link.sh CODE=PUBLIC_IP,TUNNEL_IP ...   make exactly these workers the hub's peers: link the new or replaced ones, drop the rest
#   scripts/regional-link.sh --key                         make this server's key for reaching workers, once, and say if infra/ trusts another one
#   scripts/regional-link.sh --known                       every deploy: the workers linked already, so this release's tunnel rules apply;
#                                                          links nothing new and drops nothing
#
# The list is every worker OpenTofu has, from its state, so it is complete: a peer that is not on it belongs to a worker that no longer exists.
#
# How the hub gets in. A new worker's first boot authorizes this server's public key (infra/keys/hub-regional.pub) from this server's address and
# the tunnel only, with scripts/regional-receive.sh as the only command it may run. So this script can ask a worker whether its first boot has
# finished, for its WireGuard public key, and to join the tunnel, and nothing else. Only public keys travel; each private key stays on its host.
#
# Exit status: 0 every listed worker is linked (or none is listed), 1 at least one is not, 2 a usage mistake. Never fails a production deploy.
set -uo pipefail
cd "$(dirname "$0")/.."
KEY="${HAWKEYE_REGIONAL_KEY:-$HOME/.ssh/hawkeye-regional}"
KNOWN="${HAWKEYE_REGIONAL_KNOWN_HOSTS:-$HOME/.ssh/known_hosts_regional}"
STATE="${HAWKEYE_REGIONAL_LINKS:-$HOME/.hawkeye-regional-links}"   # CODE PUBLIC_IP WIREGUARD_KEY, one line per linked worker
TRUSTED=infra/keys/hub-regional.pub
TRIES="${REGIONAL_TRIES:-45}"; WAIT="${REGIONAL_WAIT:-20}"   # up to 15 minutes for a new host's first boot (packages, Docker, the clone)
CODE_RE='^[a-z0-9][a-z0-9-]{0,18}[a-z0-9]$'
IP_RE='^([0-9]{1,3}\.){3}[0-9]{1,3}$'
TUNNEL_RE='^10\.66\.0\.([2-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-4])$'
WG_RE='^[A-Za-z0-9+/]{43}=$'

say() { echo "== regional: $*"; }
warn() { echo "!! regional: $*" >&2; }

# Makes the key the first time and reports a mismatch with the one infra/ gives new workers. Prints nothing when they agree.
ensure_key() {
  if [ ! -f "$KEY" ]; then
    install -d -m 700 "$(dirname "$KEY")"
    ssh-keygen -q -t ed25519 -N "" -C hawkeye-regional -f "$KEY" || { warn "could not make $KEY"; return 1; }
  fi
  local mine trusted
  mine="$(cut -d' ' -f1,2 "$KEY.pub")"
  trusted="$( [ -f "$TRUSTED" ] && cut -d' ' -f1,2 "$TRUSTED" || true)"
  [ "$mine" = "$trusted" ] && return 0
  warn "new regional workers will not trust this server: $TRUSTED is ${trusted:+a different key}${trusted:-missing}."
  warn "commit this line as $TRUSTED (it is a public key): $mine hawkeye-regional"
}

if [ "${1:-}" = --key ]; then ensure_key; exit 0; fi
KNOWN_ONLY=0
if [ "${1:-}" = --known ]; then
  KNOWN_ONLY=1; shift
  [ -s "$STATE" ] || exit 0   # nothing was ever linked here: the usual case until a worker exists
fi
[ $# -gt 0 ] || [ "$KNOWN_ONLY" = 1 ] || { say "no workers were given: nothing to link"; exit 0; }

# --- the list ------------------------------------------------------------------------------------------------------------------------------
declare -A PUBLIC TUNNEL
for arg in "$@"; do
  code="${arg%%=*}"; rest="${arg#*=}"; ip="${rest%%,*}"; tunnel="${rest#*,}"
  [[ "$arg" == *=*,* && "$code" =~ $CODE_RE && "$ip" =~ $IP_RE && "$tunnel" =~ $TUNNEL_RE ]] || { warn "not CODE=PUBLIC_IP,TUNNEL_IP: '$arg'"; exit 2; }
  for other in "${!TUNNEL[@]}"; do [ "${TUNNEL[$other]}" != "$tunnel" ] || { warn "$code and $other share $tunnel"; exit 2; }; done
  PUBLIC[$code]="$ip"; TUNNEL[$code]="$tunnel"
done

sudo -n true 2>/dev/null || { warn "the deploy user cannot use sudo without a password here, and WireGuard needs it (see docs/deployment.md, \"Regional worker hosts\")"; exit 1; }
ensure_key || exit 1

# The peers the hub has now, by tunnel address: "PUBKEY TUNNEL_IP".
current_peers() { sudo -n wg show wg0 dump 2>/dev/null | awk 'NR > 1 { split($4, a, "/"); print $1, a[1] }'; }
declare -A HAS   # tunnel address -> WireGuard key
while read -r pub tip; do [ -n "${tip:-}" ] && HAS[$tip]="$pub"; done < <(current_peers)
declare -A WAS_IP WAS_KEY   # from the last run: which address and key each code had
if [ -f "$STATE" ]; then while read -r c ip k; do [ -n "${k:-}" ] && { WAS_IP[$c]="$ip"; WAS_KEY[$c]="$k"; }; done < "$STATE"; fi
if [ "$KNOWN_ONLY" = 1 ]; then
  # The list is what is linked now: each remembered worker whose key is still one of the hub's peers, at that peer's tunnel address.
  for c in "${!WAS_KEY[@]}"; do
    for tip in "${!HAS[@]}"; do [ "${HAS[$tip]}" = "${WAS_KEY[$c]}" ] && { PUBLIC[$c]="${WAS_IP[$c]}"; TUNNEL[$c]="$tip"; }; done
  done
  [ ${#PUBLIC[@]} -gt 0 ] || exit 0
fi

# --- reaching a worker ---------------------------------------------------------------------------------------------------------------------
# Over its public address until the tunnel exists. Its host key is learned on first contact under the worker's code, so a different host at that
# address later is refused; a worker OpenTofu replaced has a new address, and its old entry is forgotten below.
public() { # CODE COMMAND
  ssh -i "$KEY" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o "UserKnownHostsFile=$KNOWN" \
    -o "HostKeyAlias=hawkeye-worker-$1" -o ConnectTimeout=10 -o LogLevel=ERROR "deploy@${PUBLIC[$1]}" "$2"
}
# Through the tunnel, as scripts/regional-sync.sh does: a tunnel address belongs to one WireGuard key, which proves who answers.
tunnel() { # CODE COMMAND
  ssh -i "$KEY" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 \
    -o LogLevel=ERROR "deploy@${TUNNEL[$1]}" "$2"
}

# --- what to do ----------------------------------------------------------------------------------------------------------------------------
declare -A PEER   # code -> WireGuard key it will have
NEW=()
failed=""
for code in $(printf '%s\n' "${!PUBLIC[@]}" | sort); do
  tip="${TUNNEL[$code]}"
  if [ "${WAS_IP[$code]:-}" = "${PUBLIC[$code]}" ] && [ -n "${WAS_KEY[$code]:-}" ] && [ "${HAS[$tip]:-}" = "${WAS_KEY[$code]}" ]; then
    PEER[$code]="${WAS_KEY[$code]}"; continue   # linked already, and still the same host
  fi
  # New, or replaced (a new address): a replaced host has a new host key, so forget the old one.
  [ -z "${WAS_IP[$code]:-}" ] || [ "${WAS_IP[$code]}" = "${PUBLIC[$code]}" ] || ssh-keygen -q -R "hawkeye-worker-$code" -f "$KNOWN" >/dev/null 2>&1 || true
  say "$code (${PUBLIC[$code]}): waiting for its first boot to finish"
  ready=0
  for _ in $(seq 1 "$TRIES"); do
    if public "$code" ready 2>/dev/null; then ready=1; break; fi
    sleep "$WAIT"
  done
  if [ "$ready" != 1 ]; then warn "$code did not finish its first boot or cannot be reached (/var/log/hawkeye-cloud-init.log there)"; failed="$failed $code"; continue; fi
  pub="$(public "$code" link-key 2>/dev/null | tail -1)"
  [[ "$pub" =~ $WG_RE ]] || { warn "$code did not give a WireGuard key"; failed="$failed $code"; continue; }
  PEER[$code]="$pub"; NEW+=("$code")
done

# The hub's peers become exactly the workers that exist (and have a key). A peer left over from a removed or replaced worker goes.
peers=()
for code in $(printf '%s\n' "${!PEER[@]}" | sort); do peers+=("${PEER[$code]}=${TUNNEL[$code]}"); done
if [ "$KNOWN_ONLY" = 1 ]; then
  # A deploy knows only what it linked before: a peer it doesn't know (linked by a run of the workflow since) stays as it is.
  for tip in $(printf '%s\n' "${!HAS[@]}" | sort); do
    mine=0; for code in "${!TUNNEL[@]}"; do [ "${TUNNEL[$code]}" != "$tip" ] || mine=1; done
    [ "$mine" = 1 ] || peers+=("${HAS[$tip]}=$tip")
  done
fi
want="$(printf '%s\n' "${peers[@]}" | sed '/^$/d' | sort)"
have="$(for t in "${!HAS[@]}"; do echo "${HAS[$t]}=$t"; done | sort)"
hub_pub=""
if [ ${#peers[@]} -gt 0 ] || [ "$want" != "$have" ]; then
  # Installs WireGuard here the first time and makes this server's tunnel key once; printing it changes nothing.
  hub_pub="$(sudo -n bash deploy/wireguard.sh hub-key | tail -1)"
  [[ "$hub_pub" =~ $WG_RE ]] || { warn "could not set up WireGuard on this server"; exit 1; }
  # Applied even when the peers are the same: a release may change the tunnel's rules (deploy/wireguard.sh restarts it only then).
  if [ ${#peers[@]} -gt 0 ]; then sudo -n bash deploy/wireguard.sh hub-apply "${peers[@]}" || { warn "could not update this server's tunnel"; exit 1; }
  else sudo -n bash deploy/wireguard.sh hub-apply || { warn "could not update this server's tunnel"; exit 1; }; fi
  [ "$want" = "$have" ] || say "tunnel peers: ${#peers[@]}"
fi

for code in "${NEW[@]}"; do
  public "$code" "link-apply $hub_pub" >/dev/null 2>&1 || { warn "$code could not join the tunnel"; failed="$failed $code"; unset 'PEER[$code]'; continue; }
done

# Every worker must answer through the tunnel before this returns, not only the new ones: when this release changed the tunnel's rules the
# tunnel was just restarted, and until each worker makes a new handshake (up to about a minute, it is the side that knows the other's address)
# a release sent to it would fail. TUNNEL_TRIES x 5 seconds: two minutes by default.
for code in $(printf '%s\n' "${!PEER[@]}" | sort); do
  up=0
  for _ in $(seq 1 "${TUNNEL_TRIES:-24}"); do if tunnel "$code" ready 2>/dev/null; then up=1; break; fi; sleep "${TUNNEL_WAIT:-5}"; done
  if [ "$up" = 1 ]; then
    for n in "${NEW[@]}"; do [ "$n" != "$code" ] || say "$code is linked: the tunnel works"; done
  else
    warn "$code does not answer through the tunnel (the UDP rule in infra/workers.tf, 'sudo wg show' on both ends)"; failed="$failed $code"
    for n in "${NEW[@]}"; do [ "$n" != "$code" ] || unset 'PEER[$code]'; done   # a worker linked before stays remembered; a new one is tried again
  fi
done

# Remember what is linked, so the next run skips it and can tell a replaced host from the same one.
umask 077
{ for code in $(printf '%s\n' "${!PEER[@]}" | sort); do echo "$code ${PUBLIC[$code]} ${PEER[$code]}"; done; } > "$STATE.new" && mv "$STATE.new" "$STATE"

[ -z "$failed" ] || { warn "not linked:$failed. The next infrastructure run tries again"; exit 1; }
[ ${#PUBLIC[@]} -eq 0 ] || say "${#PUBLIC[@]} worker(s) linked"
