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
    3. Copy .env.production to /home/deploy/hawkeye/.env.production (chmod 600) and ~/.hawkeye-backup.env; see docs/rebuild.md.
    4. Restore the database, then run scripts/deploy.sh and scripts/verify-egress.sh.
  EOT
}

output "stage_host" {
  description = "A real hostname (with a real certificate) that reaches this server before the production name does."
  value       = "${local.stage}.${var.domain}"
}
