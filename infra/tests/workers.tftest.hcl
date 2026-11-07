# Plans against mock providers, so it needs no tokens and touches nothing: `tofu test` from infra/.
mock_provider "vultr" {
  mock_data "vultr_os" {
    defaults = { id = 2284 }
  }
  mock_resource "vultr_instance" {
    defaults = { main_ip = "203.0.113.2" }
  }
}
mock_provider "cloudflare" {}

variables {
  cloudflare_zone_id  = "zone"
  ssh_public_key_path = "./tests/fake.pub"
  cutover             = true
}

run "the_committed_file_creates_nothing_until_a_worker_is_listed" {
  command = plan
  assert {
    condition     = length(vultr_instance.worker) == 0 && length(vultr_firewall_rule.wireguard) == 0 && length(vultr_firewall_group.worker) == 0
    error_message = "With no workers in regions.json, the plan must add nothing."
  }
}

run "each_region_gets_a_host_and_its_own_tunnel_rule" {
  command = plan
  variables {
    regions_file = "tests/two-regions.json"
  }
  assert {
    condition     = length(vultr_instance.worker) == 2 && length(vultr_firewall_rule.wireguard) == 2
    error_message = "Each region needs one host and one firewall rule, launched or not."
  }
  assert {
    condition     = vultr_instance.worker["eu-central"].region == "fra" && vultr_instance.worker["eu-central"].plan == "vc2-1c-1gb" && vultr_instance.worker["eu-central"].backups == "disabled"
    error_message = "Workers are small, stateless hosts."
  }
  assert {
    condition     = strcontains(vultr_instance.worker["eu-central"].user_data, "ALLOW_TCP=10.66.0.1:5432") && strcontains(vultr_instance.worker["eu-central"].user_data, "regional-heartbeat.sh") && !strcontains(vultr_instance.worker["eu-central"].user_data, "INFISICAL")
    error_message = "cloud-init allows the database's tunnel address, sets up the heartbeat, and holds no secret manager credentials."
  }
}

run "a_tunnel_address_cannot_be_shared" {
  command = plan
  variables {
    regions_file = "tests/shared-address.json"
  }
  expect_failures = [output.workers]
}

run "the_primary_code_is_reserved" {
  command = plan
  variables {
    regions_file = "tests/reserved-code.json"
  }
  expect_failures = [output.workers]
}

run "a_hub_address_cannot_be_given_to_a_worker" {
  command = plan
  variables {
    regions_file = "tests/hub-address.json"
  }
  expect_failures = [output.workers]
}
