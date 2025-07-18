#
# Generated by TerraSnap - https://terrasnap.confluent.io
# Date: 16 Jun 25 06:19 UTC
#
terraform {
  required_providers {
    time = {
      source  = "hashicorp/time"
      version = "0.13.1-alpha1"
    }
    external = {
      source  = "hashicorp/external"
      version = "2.3.4"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "4.0.6"
    }
    local = {
      source  = "hashicorp/local"
      version = "2.4.0"
    }
    aws = {
      source  = "hashicorp/aws"
      version = "5.80.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "3.7.2"
    }
  }
}

# providers for module reverse_proxy
provider "aws" {
  region     = var.aws_region
}
