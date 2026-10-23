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
