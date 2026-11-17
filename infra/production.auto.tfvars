# The production server as it really is (committed, read on every run, nothing secret). It was created by hand on 2026-10-17, before infra/
# existed, and is adopted into the state rather than rebuilt: see adopt.tf. Change a value here only on purpose, in a reviewed pull request;
# CI shows the plan and refuses to replace or delete anything unless that run is told to (docs/deployment.md, "Infrastructure changes").

name    = "hawkeye.observer" # its hostname and label today
plan    = "vc2-1c-2gb"       # 1 vCPU / 2 GB
os_name = "Ubuntu 26.10 x64" # not an LTS release: see docs/plan-high-availability.md

ssh_public_key_path   = "./keys/admin.pub"
deploy_authorized_key = "restrict,command=\"cd /home/deploy/hawkeye && scripts/deploy.sh\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGYQp5QH2FK86eZ4omsSEDZeNljTUeeyc1eN8DB9o7Ha github-actions-deploy"

# Vultr's backups as they were set by hand: every other day at 05:00 UTC.
backup_schedule = { type = "daily_alt_even", hour = 5 }

# The apex record points at the adopted server's own address (it has no reserved IP yet).
cutover = true

adopted_server_id = "cd6ffefd-e726-4a2f-a6c0-789b0f67b7bb"
adopted_dns_record_ids = {
  apex    = "41629454f61383a29e8a2b72cdb82b6d"
  www     = "a7d3b9835934c3cfc7c1060b23bda4c8"
  domains = "4afd02e893f596bbff5e2b281903d823"
}
