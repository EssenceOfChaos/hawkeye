# State lives in Cloudflare R2, not on anyone's laptop, so CI and people share one copy (docs/deployment.md, "Infrastructure changes").
# The bucket, endpoint and options are in state.s3.tfbackend: `tofu init -backend-config=state.s3.tfbackend`. Credentials come from
# AWS_ENDPOINT_URL_S3, AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY (an R2 API token limited to the state bucket), never from this repository.
#
# The state is encrypted before it is uploaded (OpenTofu state encryption): it holds server details and cloud-init, and whoever can read the
# bucket should still not be able to read it. The passphrase is TF_VAR_state_passphrase, kept in the secret manager and in GitHub.
terraform {
  backend "s3" {}

  encryption {
    key_provider "pbkdf2" "state" {
      passphrase = var.state_passphrase
    }
    method "aes_gcm" "state" {
      keys = key_provider.pbkdf2.state
    }
    state {
      method   = method.aes_gcm.state
      enforced = true
    }
    plan {
      method   = method.aes_gcm.state
      enforced = true
    }
  }
}

variable "state_passphrase" {
  description = "Encrypts the state and saved plans (TF_VAR_state_passphrase). At least 16 characters; losing it loses the state, so keep it in the secret manager."
  type        = string
  sensitive   = true
}
