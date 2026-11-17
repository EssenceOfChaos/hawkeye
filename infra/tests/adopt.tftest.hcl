# The production server and records are adopted (imported), not created: plans against mock providers, so it needs no tokens and touches nothing.
mock_provider "vultr" {
  mock_data "vultr_os" {
    defaults = { id = 2838 }
  }
  mock_resource "vultr_instance" {
    defaults = { main_ip = "45.77.216.247" }
  }
}
mock_provider "cloudflare" {}

variables {
  cloudflare_zone_id = "zone"
  state_passphrase   = "test-passphrase-not-secret"
  import_adopted     = false # tofu test cannot import; this checks the configuration the import is matched against
}

run "production_adopts_the_existing_server_with_a_firewall_but_no_reserved_ip" {
  command = plan
  assert {
    condition     = length(vultr_reserved_ip.main) == 0 && length(cloudflare_dns_record.stage) == 0
    error_message = "An adopted server keeps its own address: no reserved IP and no staging name."
  }
  assert {
    condition     = length(vultr_firewall_rule.ssh) == 1 && vultr_firewall_rule.http.port == "80" && vultr_firewall_rule.https.port == "443"
    error_message = "The adopted server's firewall group admits SSH, HTTP and HTTPS, the ports it serves today."
  }
  assert {
    condition     = vultr_instance.server.hostname == "hawkeye.observer" && vultr_instance.server.plan == "vc2-1c-2gb"
    error_message = "The adopted server must be described as it is, or the plan would reinstall or resize it."
  }
  assert {
    condition     = toset(keys(cloudflare_dns_record.prod)) == toset(["apex", "www", "domains"])
    error_message = "The three production records are managed."
  }
}

run "a_rebuilt_production_server_gets_a_reserved_ip_and_firewall" {
  command = plan
  variables {
    adopted_server_id = ""
  }
  assert {
    condition     = length(vultr_reserved_ip.main) == 1
    error_message = "A server OpenTofu creates gets its reserved IP."
  }
}

run "workers_can_join_the_adopted_server_now_it_has_a_firewall_group" {
  command = plan
  variables {
    regions_file = "tests/two-regions.json"
  }
  assert {
    condition     = length(vultr_instance.worker) == 2 && length(vultr_firewall_rule.wireguard) == 2
    error_message = "Each worker gets a host and a tunnel rule in the production server's firewall group."
  }
}
