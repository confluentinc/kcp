resource "random_string" "suffix" {
  length  = 4
  special = false
  numeric = false
  upper   = false
}

resource "confluent_environment" "environment" {
  display_name = var.confluent_cloud_environment_name

  stream_governance {
    package = "ADVANCED"
  }
}

locals {
  availability = (
    var.confluent_cloud_cluster_type == "enterprise" ? "HIGH" :
    var.confluent_cloud_cluster_type == "dedicated" ? "MULTI_ZONE" :
    "SINGLE_ZONE"
  )
}

resource "confluent_kafka_cluster" "cluster" {
  display_name = var.confluent_cloud_cluster_name
  availability = local.availability
  cloud        = var.confluent_cloud_provider
  region       = var.confluent_cloud_region

  dynamic "enterprise" {
    for_each = var.confluent_cloud_cluster_type == "enterprise" ? [1] : []

    content {}
  }

  dynamic "dedicated" {
    for_each = var.confluent_cloud_cluster_type == "dedicated" ? [1] : []

    content {
      cku = 1
    }
  }

  environment {
    id = confluent_environment.environment.id
  }
}
