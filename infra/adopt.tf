# Adopting the production server and DNS records that were created by hand (2026-10-17), so OpenTofu manages them from now on without rebuilding
# anything. Import only reads: the first apply records these in the state, and the plan around it must show no replacement or deletion (CI checks).
# Only the production workspace imports; rehearsal workspaces create their own servers as before. Once the first apply has recorded them, the two
# import blocks do nothing and can be deleted.
#
# An adopted server is described as it is, except for what has since been added to it in reviewed changes: the Vultr firewall group
# (2026-11-17). It has no reserved IP yet (docs/plan-high-availability.md, step 1). Once adopted_server_id is cleared, a rebuild (scripts/rebuild.sh) creates
# them as for any new server.

variable "adopted_server_id" {
  description = "Vultr instance id of a production server created outside OpenTofu, to import instead of creating one. Empty: create a server."
  type        = string
  default     = ""
}

variable "adopted_dns_record_ids" {
  description = "Cloudflare record ids of the production records (apex, www, domains) to import. Only used with adopted_server_id."
  type        = map(string)
  default     = {}
}

variable "import_adopted" {
  description = "Import the adopted resources. Only the tests turn it off: `tofu test` cannot import (it crashes OpenTofu 1.13 instead of failing)."
  type        = bool
  default     = true
}

locals {
  adopted = local.is_prod && var.adopted_server_id != ""
  imports = local.adopted && var.import_adopted
  # The address the production name and the deploy secret point at: the reserved IP, or an adopted server's own address.
  server_ip = local.adopted ? vultr_instance.server.main_ip : vultr_reserved_ip.main[0].subnet
}

import {
  for_each = local.imports ? toset(["server"]) : toset([])
  to       = vultr_instance.server
  id       = var.adopted_server_id
}

import {
  for_each = local.imports ? var.adopted_dns_record_ids : {}
  to       = cloudflare_dns_record.prod[each.key]
  id       = "${var.cloudflare_zone_id}/${each.value}"
}
