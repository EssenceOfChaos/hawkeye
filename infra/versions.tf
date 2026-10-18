terraform {
  required_version = ">= 1.6"
  required_providers {
    vultr = {
      source  = "vultr/vultr"
      version = "~> 2.26"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5.0"
    }
  }
}

# The API key is read from the VULTR_API_KEY environment variable; it is never written in code or state.
provider "vultr" {}

# The token is read from CLOUDFLARE_API_TOKEN (scripts/rebuild.sh maps it from CF_DNS_API_TOKEN). Scope it to this one zone with "DNS Edit".
provider "cloudflare" {}
