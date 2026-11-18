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
  state_passphrase    = "test-passphrase-not-secret"
  # These tests are about a server OpenTofu creates; production.auto.tfvars adopts one (tests/adopt.tftest.hcl covers that).
  adopted_server_id     = ""
  hub_regional_key_path = "tests/hub-regional.pub"
}

run "nothing_is_created_until_a_worker_is_listed" {
  command = plan
  variables {
    regions_file = "tests/no-workers.json"
  }
  assert {
    condition     = length(vultr_instance.worker) == 0 && length(vultr_firewall_rule.wireguard) == 0 && length(vultr_firewall_group.worker) == 0
    error_message = "With no workers in regions.json, the plan must add nothing."
  }
}

run "the_committed_file_lists_amsterdam_and_los_angeles_with_the_committed_key" {
  command = plan
  variables {
    hub_regional_key_path = "keys/hub-regional.pub"
  }
  assert {
    condition     = toset(keys(vultr_instance.worker)) == toset(["eu-central", "us-west"]) && vultr_instance.worker["eu-central"].region == "ams" && vultr_instance.worker["us-west"].region == "lax"
    error_message = "Production's workers are Amsterdam and Los Angeles."
  }
  assert {
    condition     = strcontains(vultr_instance.worker["us-west"].user_data, trimspace(file("keys/hub-regional.pub")))
    error_message = "Workers trust the production server's committed key."
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

run "the_production_server_may_reach_a_new_worker_only_through_the_receiver" {
  command = plan
  variables {
    regions_file = "tests/two-regions.json"
  }
  assert {
    condition     = strcontains(vultr_instance.worker["eu-central"].user_data, "restrict,command=\\\"/home/deploy/hawkeye/scripts/regional-receive.sh\\\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHViLXJlZ2lvbmFsLWtleS1mb3ItdGVzdHMtb25seQ hawkeye-regional")
    error_message = "The production server's key is installed with the receiver as its only command."
  }
  assert {
    condition     = strcontains(vultr_instance.worker["eu-central"].user_data, "from=\\\"10.66.0.1,")
    error_message = "The production server's key is accepted only from the tunnel and the production server's address."
  }
  assert {
    condition     = strcontains(vultr_instance.worker["eu-central"].user_data, "REGION=eu-central") && strcontains(vultr_instance.worker["eu-central"].user_data, "TUNNEL_IP=10.66.0.2") && strcontains(vultr_instance.worker["ap-southeast"].user_data, "TUNNEL_IP=10.66.0.3")
    error_message = "Each worker knows its own code and tunnel address, so joining the tunnel needs only the production server's public key."
  }
}

run "no_worker_is_created_before_the_production_servers_key_is_committed" {
  command = plan
  variables {
    regions_file          = "tests/two-regions.json"
    hub_regional_key_path = "tests/missing.pub"
  }
  expect_failures = [vultr_instance.worker]
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
