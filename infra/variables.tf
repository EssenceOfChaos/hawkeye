variable "region" {
  description = "Vultr region. ewr is New Jersey (US), the same place as the backup bucket."
  type        = string
  default     = "ewr"
}

variable "plan" {
  description = "Vultr plan. vc2-2c-4gb is 2 vCPU / 4 GB ($20/month), enough to build the images on the server; vc2-1c-2gb works but is tight."
  type        = string
  default     = "vc2-2c-4gb"
}

variable "os_name" {
  description = "Exact Vultr OS name, looked up to an id (so no hard-coded id goes stale). Use an LTS release for production."
  type        = string
  default     = "Ubuntu 24.04 LTS x64"
}

variable "name" {
  description = "Server hostname and label. Changing the hostname later replaces the server (the provider treats it as an OS reinstall)."
  type        = string
  default     = "hawkeye"
}

variable "ssh_public_key_path" {
  description = "Your public SSH key. It is installed for root at creation and for the deploy user by cloud-init."
  type        = string
  default     = "~/.ssh/id_ed25519.pub"
}

variable "deploy_authorized_key" {
  description = <<-EOT
    Optional: the full authorized_keys line for GitHub Actions deploys, for example
    restrict,command="cd /home/deploy/hawkeye && scripts/deploy.sh" ssh-ed25519 AAAA... github-actions-deploy
    (a public key plus its forced command; see docs/deployment.md, "Automated deploys").
  EOT
  type        = string
  default     = ""
}

variable "repo_url" {
  description = "Git repository cloned to /home/deploy/hawkeye."
  type        = string
  default     = "https://github.com/EssenceOfChaos/hawkeye.git"
}

variable "dns_resolvers" {
  description = "Resolver IPs the worker containers may use through the egress firewall (Vultr's resolvers; `resolvectl dns` on the server shows them)."
  type        = list(string)
  default     = ["108.61.10.10", "108.61.115.10"]
}

variable "ssh_allowed_cidrs" {
  description = "Who may reach SSH (port 22). GitHub-hosted deploy runners have no fixed addresses, so the default is open; the server only accepts keys."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "enable_ipv6" {
  type    = bool
  default = false
}

variable "enable_backups" {
  description = "Vultr's own whole-server backups (extra charge). They complement, not replace, the nightly database dumps in object storage."
  type        = bool
  default     = true
}

variable "swap_gb" {
  description = "Swap file size. Building the Go and Node images on a 2 GB server needs it."
  type        = number
  default     = 2
}

variable "cloudflare_zone_id" {
  description = "Cloudflare zone id of the domain (TF_VAR_cloudflare_zone_id; scripts/rebuild.sh sets it from CF_ZONE_ID)."
  type        = string
}

variable "domain" {
  description = "The site's domain. Its apex A record and www CNAME are managed here; every other record (mail, Resend, Email Routing) is left alone."
  type        = string
  default     = "hawkeye.observer"
}

variable "cutover" {
  description = <<-EOT
    false: the apex A record keeps pointing at old_ip (the server in service today); a "staging" name points at the new server for testing.
    true: the apex A record points at this server. scripts/rebuild.sh flips it (and records the choice in live.auto.tfvars so a later apply
    cannot flip it back by accident).
  EOT
  type        = bool
  default     = false
}

variable "old_ip" {
  description = "Where the apex A record pointed before the cutover (kept so the cutover can be rolled back). Set by scripts/rebuild.sh."
  type        = string
  default     = ""
}

variable "dns_ttl" {
  description = "TTL in seconds for the records managed here. 60 means a cutover or rollback takes effect within a minute."
  type        = number
  default     = 60
}
