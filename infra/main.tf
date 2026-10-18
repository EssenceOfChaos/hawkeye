locals {
  # The default workspace is production ("hawkeye"); any other workspace (for example a disaster-recovery drill) gets its own suffixed server.
  server_name = terraform.workspace == "default" ? var.name : "${var.name}-${terraform.workspace}"
  ssh_key     = trimspace(file(pathexpand(var.ssh_public_key_path)))
}

data "vultr_os" "ubuntu" {
  filter {
    name   = "name"
    values = [var.os_name]
  }
}

resource "vultr_ssh_key" "admin" {
  name    = "${local.server_name}-admin"
  ssh_key = local.ssh_key
}

# Vultr's network firewall drops everything that is not listed. ufw on the server is a second layer.
resource "vultr_firewall_group" "web" {
  description = "${local.server_name}: SSH, HTTP, HTTPS"
}

resource "vultr_firewall_rule" "ssh" {
  for_each          = toset(var.ssh_allowed_cidrs)
  firewall_group_id = vultr_firewall_group.web.id
  protocol          = "tcp"
  ip_type           = "v4"
  subnet            = split("/", each.value)[0]
  subnet_size       = tonumber(split("/", each.value)[1])
  port              = "22"
  notes             = "ssh"
}

resource "vultr_firewall_rule" "http" {
  firewall_group_id = vultr_firewall_group.web.id
  protocol          = "tcp"
  ip_type           = "v4"
  subnet            = "0.0.0.0"
  subnet_size       = 0
  port              = "80"
  notes             = "http (Caddy, ACME and redirect)"
}

resource "vultr_firewall_rule" "https" {
  firewall_group_id = vultr_firewall_group.web.id
  protocol          = "tcp"
  ip_type           = "v4"
  subnet            = "0.0.0.0"
  subnet_size       = 0
  port              = "443"
  notes             = "https"
}

resource "vultr_firewall_rule" "http_v6" {
  count             = var.enable_ipv6 ? 1 : 0
  firewall_group_id = vultr_firewall_group.web.id
  protocol          = "tcp"
  ip_type           = "v6"
  subnet            = "::"
  subnet_size       = 0
  port              = "80"
}

resource "vultr_firewall_rule" "https_v6" {
  count             = var.enable_ipv6 ? 1 : 0
  firewall_group_id = vultr_firewall_group.web.id
  protocol          = "tcp"
  ip_type           = "v6"
  subnet            = "::"
  subnet_size       = 0
  port              = "443"
}

# A reserved IP stays yours when the server is replaced: the DNS record and the GitHub secret DEPLOY_HOST never change.
resource "vultr_reserved_ip" "main" {
  region  = var.region
  ip_type = "v4"
  label   = "${local.server_name}-ip"
}

resource "vultr_instance" "server" {
  region            = var.region
  plan              = var.plan
  os_id             = data.vultr_os.ubuntu.id
  hostname          = local.server_name
  label             = local.server_name
  tags              = ["hawkeye", terraform.workspace == "default" ? "production" : terraform.workspace]
  ssh_key_ids       = [vultr_ssh_key.admin.id]
  firewall_group_id = vultr_firewall_group.web.id
  reserved_ip_id    = vultr_reserved_ip.main.id
  enable_ipv6       = var.enable_ipv6
  backups           = var.enable_backups ? "enabled" : "disabled"
  activation_email  = false
  user_scheme       = "root"

  dynamic "backups_schedule" {
    for_each = var.enable_backups ? [1] : []
    content {
      type = "daily"
      hour = 8 # UTC, away from the 03:15 server-time database dump
    }
  }

  # cloud-init runs once, at first boot. Nothing secret goes here: user_data can be read by anyone holding the Vultr API key.
  # Pass PLAIN text: the provider base64-encodes user_data itself. (Encoding it here too double-encodes it, cloud-init then ignores it as
  # "non-multipart userdata", and the server boots with none of the setup: no deploy user, password SSH still on.)
  user_data = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    ssh_public_key        = local.ssh_key
    deploy_authorized_key = var.deploy_authorized_key
    repo_url              = var.repo_url
    dns_resolvers         = join(",", var.dns_resolvers)
    swap_gb               = var.swap_gb
  })

  lifecycle {
    # The server is replaceable, not editable: to apply a changed cloud-init, replace it on purpose with `tofu apply -replace=vultr_instance.server`.
    ignore_changes = [user_data]
  }
}

# ---- DNS (Cloudflare) -------------------------------------------------------------------------------------------------------------------
# Production workspace: the apex A record and www CNAME (imported from what exists today) plus a "staging" name for testing the new server
# before the cutover. Any other workspace (a rehearsal) only gets its own "<workspace>" name; it never touches the real site's records.
# All records are DNS-only (not proxied): see the notes on the Cloudflare proxy in docs/deployment.md.
locals {
  is_prod = terraform.workspace == "default"
  stage   = local.is_prod ? "staging" : terraform.workspace
  apex_ip = var.cutover ? vultr_reserved_ip.main.subnet : var.old_ip
}

resource "cloudflare_dns_record" "apex" {
  count   = local.is_prod ? 1 : 0
  zone_id = var.cloudflare_zone_id
  name    = var.domain
  type    = "A"
  content = local.apex_ip
  ttl     = var.dns_ttl
  proxied = false
  comment = "HawkEye production (managed by infra/)"

  lifecycle {
    precondition {
      condition     = var.cutover || var.old_ip != ""
      error_message = "old_ip must be set until the cutover (scripts/rebuild.sh writes it to live.auto.tfvars)."
    }
  }
}

resource "cloudflare_dns_record" "www" {
  count   = local.is_prod ? 1 : 0
  zone_id = var.cloudflare_zone_id
  name    = "www.${var.domain}"
  type    = "CNAME"
  content = var.domain
  ttl     = var.dns_ttl
  proxied = false
  comment = "HawkEye www redirect (managed by infra/)"
}

resource "cloudflare_dns_record" "stage" {
  zone_id = var.cloudflare_zone_id
  name    = "${local.stage}.${var.domain}"
  type    = "A"
  content = vultr_reserved_ip.main.subnet
  ttl     = var.dns_ttl
  proxied = false
  comment = "HawkEye ${local.stage} (managed by infra/)"
}
