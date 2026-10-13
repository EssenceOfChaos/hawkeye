#!/usr/bin/env bash
# Egress firewall for the check workers (Linux hosts running Docker). DEFENCE IN DEPTH for SSRF: the application already refuses private
# destinations (internal/safehttp), this makes the network refuse them too, so a bug in one layer is not a breach.
#
# What it enforces for containers on the worker network (default 172.30.10.0/24, see `checks` in docker-compose.prod.yml):
#   * traffic to private, loopback, link-local (cloud metadata!), CGNAT, multicast and reserved ranges is DROPPED,
#   * except the few internal endpoints the worker legitimately needs, which you list explicitly (database, DNS resolvers, mail relay),
#   * traffic to the DOCKER HOST ITSELF is dropped (INPUT chain), so a worker cannot reach services bound on the host,
#   * everything else (the public internet) is allowed.
#
#   sudo ALLOW_TCP="10.0.0.5:5432,172.30.10.5:5432,203.0.113.10:587" ALLOW_DNS="10.0.0.2,1.1.1.1" deploy/egress-filter.sh apply
#   sudo deploy/egress-filter.sh status | remove
#
# ALLOW_TCP : comma list of ip:port the worker must reach (PostgreSQL, SMTP relay by IP). Use IPs, not names.
# ALLOW_DNS : comma list of DNS resolver IPs (port 53 udp+tcp).
# WORKER_SUBNET: the worker network's subnet (default 172.30.10.0/24).
# Rules live in the chains HAWKEYE-WORKER-EGRESS / HAWKEYE-WORKER-INPUT, are removed by `remove`, and are NOT persistent across reboots
# (re-run at boot, e.g. from a systemd unit ordered after docker.service). IPv6 is not routed to the worker network by default; keep it that way.
# NOT tested on the development machine (macOS): verify on the real host with scripts/verify-egress.sh.
set -euo pipefail
ACTION="${1:-}"; SUBNET="${WORKER_SUBNET:-172.30.10.0/24}"
EGRESS=HAWKEYE-WORKER-EGRESS; INPUTC=HAWKEYE-WORKER-INPUT
PRIVATE_RANGES="0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.168.0.0/16 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/4 240.0.0.0/4"

need_root() { [ "$(id -u)" = 0 ] || { echo "run as root (sudo)" >&2; exit 1; }; }
ipt() { iptables -w "$@"; }

remove() {
  ipt -D DOCKER-USER -s "$SUBNET" -j "$EGRESS" 2>/dev/null || true
  ipt -D INPUT -s "$SUBNET" -j "$INPUTC" 2>/dev/null || true
  for c in "$EGRESS" "$INPUTC"; do ipt -F "$c" 2>/dev/null || true; ipt -X "$c" 2>/dev/null || true; done
}

case "$ACTION" in
  apply)
    need_root; command -v iptables >/dev/null || { echo "iptables not found" >&2; exit 1; }
    ipt -nL DOCKER-USER >/dev/null 2>&1 || { echo "DOCKER-USER chain missing: is Docker running (and using iptables)?" >&2; exit 1; }
    : "${ALLOW_DNS:?set ALLOW_DNS to your resolver IP(s)}"
    remove
    ipt -N "$EGRESS"; ipt -N "$INPUTC"
    # --- forwarded traffic (container -> anywhere off-host or to other containers)
    ipt -A "$EGRESS" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    IFS=, read -ra TCP <<< "${ALLOW_TCP:-}"
    for e in "${TCP[@]:-}"; do [ -n "$e" ] && ipt -A "$EGRESS" -p tcp -d "${e%:*}" --dport "${e##*:}" -j ACCEPT; done
    IFS=, read -ra DNS <<< "$ALLOW_DNS"
    for d in "${DNS[@]}"; do ipt -A "$EGRESS" -p udp -d "$d" --dport 53 -j ACCEPT; ipt -A "$EGRESS" -p tcp -d "$d" --dport 53 -j ACCEPT; done
    for r in $PRIVATE_RANGES; do ipt -A "$EGRESS" -d "$r" -j DROP; done
    ipt -A "$EGRESS" -j ACCEPT
    ipt -I DOCKER-USER 1 -s "$SUBNET" -j "$EGRESS"
    # --- traffic addressed to the host itself
    ipt -A "$INPUTC" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    for d in "${DNS[@]}"; do ipt -A "$INPUTC" -p udp -d "$d" --dport 53 -j ACCEPT; done
    ipt -A "$INPUTC" -j DROP
    ipt -I INPUT 1 -s "$SUBNET" -j "$INPUTC"
    echo "egress filter applied for $SUBNET"; ;;
  remove) need_root; remove; echo "egress filter removed" ;;
  status) ipt -S "$EGRESS" 2>/dev/null || echo "not applied"; ipt -S "$INPUTC" 2>/dev/null || true ;;
  *) echo "usage: $0 apply|remove|status" >&2; exit 2 ;;
esac
