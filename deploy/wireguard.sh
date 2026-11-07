#!/usr/bin/env bash
# The private link between the production server (the hub) and the regional workers. scripts/regional.sh link sends this file over SSH and runs it as root
# on each host; there is no need to run it by hand. docs/deployment.md, "Regional worker hosts", explains the design.
#
#   wireguard.sh hub-key                         install WireGuard, make this host's key once, print the public key
#   wireguard.sh hub-apply PUBKEY=TUNNEL_IP ...  write the hub's config with exactly these peers and apply it (running tunnels are not interrupted)
#   wireguard.sh worker-key                      as hub-key, on a worker
#   wireguard.sh worker-apply HUB_PUBKEY HUB_IP TUNNEL_IP
#   wireguard.sh render-hub PUBKEY=TUNNEL_IP ... print the hub's config (changes nothing)
#   wireguard.sh render-worker HUB_PUBKEY HUB_IP TUNNEL_IP
#   wireguard.sh with-key                        add this host's private key to the config on stdin, as hub-apply and worker-apply do
#
# What a worker can reach through the tunnel is one thing: PostgreSQL. The tunnel address forwards port 5432 to the database container, and every other
# packet arriving on the tunnel is dropped, including one worker trying to reach another. These are firewall rules on the tunnel and no change to the
# Compose stack, on purpose: publishing the port on the tunnel address would stop the database from starting whenever the tunnel is down.
#
# Private keys are created on the host that uses them and never leave it: only that host's root-only /etc/wireguard holds them, not OpenTofu state,
# cloud-init or the secret manager.
# The database has no TLS (docker-compose.local-db.yml), so the tunnel is what encrypts this traffic.
set -euo pipefail
IFACE=wg0
PORT=51820
HUB_TUNNEL_IP=10.66.0.1
DB_ADDR="${HAWKEYE_DB_ADDR:-172.30.10.5}"   # fixed in docker-compose.local-db.yml
DB_PORT=5432
DIR=/etc/wireguard

valid_key() { [[ "$1" =~ ^[A-Za-z0-9+/]{43}=$ ]] || { echo "not a WireGuard public key: $1" >&2; exit 2; }; }
valid_ip() { [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { echo "not an IPv4 address: $1" >&2; exit 2; }; }

render_hub() {
  cat <<CONF
# Managed by scripts/regional.sh: edits here are overwritten.
[Interface]
Address = $HUB_TUNNEL_IP/24
ListenPort = $PORT
PostUp = iptables -w -t nat -A PREROUTING -i %i -d $HUB_TUNNEL_IP -p tcp --dport $DB_PORT -j DNAT --to-destination $DB_ADDR:$DB_PORT
PostUp = iptables -w -I DOCKER-USER 1 -i %i -j DROP
PostUp = iptables -w -I DOCKER-USER 1 -i %i -d $DB_ADDR -p tcp --dport $DB_PORT -j ACCEPT
PostUp = iptables -w -t mangle -A FORWARD -o %i -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
PostDown = iptables -w -t nat -D PREROUTING -i %i -d $HUB_TUNNEL_IP -p tcp --dport $DB_PORT -j DNAT --to-destination $DB_ADDR:$DB_PORT
PostDown = iptables -w -D DOCKER-USER -i %i -d $DB_ADDR -p tcp --dport $DB_PORT -j ACCEPT
PostDown = iptables -w -D DOCKER-USER -i %i -j DROP
PostDown = iptables -w -t mangle -D FORWARD -o %i -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
CONF
  local peer pub ip
  for peer in "$@"; do
    pub="${peer%=*}"; ip="${peer##*=}"
    valid_key "$pub"; valid_ip "$ip"
    printf '\n[Peer]\nPublicKey = %s\nAllowedIPs = %s/32\n' "$pub" "$ip"
  done
}

render_worker() {
  local hub_pub="$1" hub_ip="$2" tunnel_ip="$3"
  valid_key "$hub_pub"; valid_ip "$hub_ip"; valid_ip "$tunnel_ip"
  cat <<CONF
# Managed by scripts/regional.sh: edits here are overwritten.
[Interface]
Address = $tunnel_ip/32
PostUp = iptables -w -t mangle -A FORWARD -o %i -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
PostDown = iptables -w -t mangle -D FORWARD -o %i -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu

[Peer]
PublicKey = $hub_pub
Endpoint = $hub_ip:$PORT
AllowedIPs = $HUB_TUNNEL_IP/32
PersistentKeepalive = 25
CONF
}

ensure_key() {
  command -v wg >/dev/null || { apt-get update -qq >&2; DEBIAN_FRONTEND=noninteractive apt-get install -y -qq wireguard-tools >&2; }
  install -d -m 700 "$DIR"
  if [ ! -f "$DIR/private.key" ]; then (umask 077; wg genkey > "$DIR/private.key"); fi
  wg pubkey < "$DIR/private.key"
}

# Adds this host's private key to a rendered config. The key must be in the file: `wg syncconf` makes the interface match the config exactly, so a config
# without a key would wipe it from the running interface and drop every tunnel. awk reads it from the key file, which keeps it out of any command line.
with_key() { awk -v keyfile="$DIR/private.key" '{ print } /^\[Interface\]$/ { getline key < keyfile; print "PrivateKey = " key }'; }

install_config() { # stdin -> the config, atomically
  (umask 077; with_key > "$DIR/$IFACE.conf.new")
  mv "$DIR/$IFACE.conf.new" "$DIR/$IFACE.conf"
  # The rules in PostUp use Docker's chain, so at boot the tunnel has to come up after Docker.
  install -d "/etc/systemd/system/wg-quick@$IFACE.service.d"
  printf '[Unit]\nAfter=docker.service\n' > "/etc/systemd/system/wg-quick@$IFACE.service.d/after-docker.conf"
  systemctl daemon-reload
  systemctl enable --quiet "wg-quick@$IFACE"
}

ACTION="${1:-}"; [ $# -gt 0 ] && shift
case "$ACTION" in
  render-hub) render_hub "$@" ;;
  render-worker) render_worker "$@" ;;
  with-key) with_key ;;
  hub-key|worker-key) ensure_key ;;
  hub-apply)
    render_hub "$@" | install_config
    ufw allow "$PORT/udp" >/dev/null   # Vultr's network firewall admits only the workers (infra/workers.tf)
    if systemctl is-active --quiet "wg-quick@$IFACE"; then
      wg syncconf "$IFACE" <(wg-quick strip "$IFACE")   # peers change, live tunnels stay up
    else
      systemctl start "wg-quick@$IFACE"
    fi ;;
  worker-apply)
    render_worker "$@" | install_config
    systemctl restart "wg-quick@$IFACE" ;;
  *) echo "usage: $0 hub-key|hub-apply|worker-key|worker-apply|render-hub|render-worker" >&2; exit 2 ;;
esac
