output "public_ip" {
  description = "The reserved IPv4 address: point the DNS A record here, and use it as the DEPLOY_HOST GitHub secret."
  value       = vultr_reserved_ip.main.subnet
}

output "ssh" {
  value = "ssh deploy@${vultr_reserved_ip.main.subnet}"
}

output "server_id" {
  value = vultr_instance.server.id
}

output "next_steps" {
  value = <<-EOT
    1. Wait about 3 minutes for first-boot setup, then: ssh deploy@${vultr_reserved_ip.main.subnet}   (log: /var/log/hawkeye-cloud-init.log)
    2. Point DNS (A record) at ${vultr_reserved_ip.main.subnet}.
    3. Settings: nothing to do if you use scripts/rebuild.sh: `create` gives the server its secret manager credentials and `stage` has it fetch every setting itself (docs/secrets.md). Only if you apply this by hand, without rebuild.sh: copy ~/.hawkeye-secrets/.hawkeye-infisical.env to ~/.hawkeye-infisical.env on the server (chmod 600) and run scripts/sync-secrets.sh in /home/deploy/hawkeye.
    4. Restore the database, then run scripts/deploy.sh and scripts/verify-egress.sh.
  EOT
}

output "stage_host" {
  description = "A real hostname (with a real certificate) that reaches this server before the production name does."
  value       = "${local.stage}.${var.domain}"
}

output "workers" {
  description = "Regional worker hosts, read by scripts/regional.sh."
  value = { for code, w in local.workers : code => {
    ip           = vultr_instance.worker[code].main_ip
    tunnel_ip    = w.tunnel_ip
    vultr_region = w.vultr_region
  } }

  # The same rules as scripts/regions.py, so a bad file stops at the plan as well as in CI.
  precondition {
    condition     = alltrue([for code, w in local.regions.workers : can(regex("^[a-z0-9][a-z0-9-]{0,18}[a-z0-9]$", code)) && code != "primary"])
    error_message = "regions.json: region codes are 2 to 20 lowercase letters, digits or hyphens, and \"primary\" is the production server's own code."
  }
  precondition {
    condition     = alltrue([for w in values(local.regions.workers) : can(regex("^10\\.66\\.0\\.([2-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-4])$", w.tunnel_ip))])
    error_message = "regions.json: tunnel_ip must be 10.66.0.2 to 10.66.0.254 (10.66.0.1 is the production server)."
  }
  precondition {
    condition     = length(distinct([for w in values(local.regions.workers) : w.tunnel_ip])) == length(keys(local.regions.workers))
    error_message = "regions.json: each worker needs its own tunnel_ip."
  }
}
