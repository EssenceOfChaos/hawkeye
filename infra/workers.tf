# Regional check workers, one per entry in regions.json (the one list of locations: scripts/regions.py reads it too). Nothing here exists until the file
# lists a worker, and only the production workspace gets them: a rehearsal must never point a worker at anything real. docs/deployment.md,
# "Regional worker hosts", has the whole sequence: a pull request to regions.json, and the Infrastructure workflow does the rest.
locals {
  regions        = jsondecode(file("${path.module}/${var.regions_file}"))
  workers        = { for code, w in local.regions.workers : code => w if local.is_prod }
  hub_tunnel_ip  = "10.66.0.1"
  wireguard_port = 51820
  # The production server's key for reaching workers (made there by scripts/regional-link.sh, which prints it until it is committed here).
  hub_key_file = "${path.module}/${var.hub_regional_key_path}"
  hub_key      = fileexists(local.hub_key_file) ? trimspace(file(local.hub_key_file)) : ""
}

# Workers accept SSH and nothing else: they dial out to the tunnel and to customers' sites, and serve no one.
resource "vultr_firewall_group" "worker" {
  count       = length(local.workers) > 0 ? 1 : 0
  description = "${local.server_name}-worker: SSH only"
}

resource "vultr_firewall_rule" "worker_ssh" {
  for_each          = length(local.workers) > 0 ? toset(var.ssh_allowed_cidrs) : toset([])
  firewall_group_id = vultr_firewall_group.worker[0].id
  protocol          = "tcp"
  ip_type           = "v4"
  subnet            = split("/", each.value)[0]
  subnet_size       = tonumber(split("/", each.value)[1])
  port              = "22"
  notes             = "ssh"
}

resource "vultr_instance" "worker" {
  for_each          = local.workers
  region            = each.value.vultr_region
  plan              = var.worker_plan
  os_id             = data.vultr_os.ubuntu.id
  hostname          = "${local.server_name}-worker-${each.key}"
  label             = "${local.server_name}-worker-${each.key}"
  tags              = ["hawkeye", "worker", each.key]
  ssh_key_ids       = [vultr_ssh_key.admin.id]
  firewall_group_id = vultr_firewall_group.worker[0].id
  enable_ipv6       = false
  backups           = "disabled" # it stores nothing: a lost worker is replaced, not restored
  activation_email  = false
  user_scheme       = "root"

  # As in main.tf: no secrets here (public keys and addresses only), and plain text because the provider base64-encodes user_data itself.
  user_data = templatefile("${path.module}/worker-cloud-init.yaml.tftpl", {
    ssh_public_key     = local.ssh_key
    hub_authorized_key = "from=\"${local.hub_tunnel_ip},${local.server_ip}\",restrict,command=\"/home/deploy/hawkeye/scripts/regional-receive.sh\" ${local.hub_key}"
    region             = each.key
    tunnel_ip          = each.value.tunnel_ip
    hub_ip             = local.server_ip
    repo_url           = var.repo_url
    dns_resolvers      = join(",", var.dns_resolvers)
    hub_tunnel_ip      = local.hub_tunnel_ip
    swap_gb            = var.worker_swap_gb
  })

  lifecycle {
    # Read only at creation (see main.tf): a worker made before a change to these keeps its own, and is replaced on purpose to get the new ones.
    ignore_changes = [user_data, ssh_key_ids, activation_email, os_id]
    precondition {
      condition     = can(regex("^ssh-ed25519 [A-Za-z0-9+/=]+ hawkeye-regional$", local.hub_key))
      error_message = "infra/keys/hub-regional.pub must hold the production server's key for reaching workers before a worker is created: the deploy log prints the line to commit."
    }
  }
}

# Only the workers may reach the tunnel port on the production server. WireGuard ignores unauthenticated packets anyway; this keeps it invisible.
resource "vultr_firewall_rule" "wireguard" {
  for_each          = local.workers
  firewall_group_id = vultr_firewall_group.web.id
  protocol          = "udp"
  ip_type           = "v4"
  subnet            = vultr_instance.worker[each.key].main_ip
  subnet_size       = 32
  port              = tostring(local.wireguard_port)
  notes             = "wireguard from worker ${each.key}"
}
