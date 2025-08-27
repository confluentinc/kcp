terraform {
  required_providers {
    confluent = {
      source  = "confluentinc/confluent"
      version = "2.23.0"
    }

    random = {
      source  = "hashicorp/random"
      version = "3.7.2"
    }
  }
}
