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

data "confluent_schema_registry_cluster" "schema_registry" {
  environment {
    id = confluent_environment.environment.id
  }
}

resource "confluent_service_account" "app-manager" {
  display_name = "app-manager-${random_string.suffix.result}"
  description  = "Service account to manage the ${var.confluent_cloud_environment_name} environment."
}

resource "confluent_role_binding" "subject-resource-owner" {
  principal   = "User:${confluent_service_account.app-manager.id}"
  role_name   = "ResourceOwner"
  crn_pattern = "${data.confluent_schema_registry_cluster.schema_registry.resource_name}/subject=*"
}

resource "confluent_role_binding" "app-manager-kafka-cluster-admin" {
  principal   = "User:${confluent_service_account.app-manager.id}"
  role_name   = "CloudClusterAdmin"
  crn_pattern = confluent_kafka_cluster.cluster.rbac_crn
}

resource "confluent_role_binding" "app-manager-kafka-data-steward" {
  principal   = "User:${confluent_service_account.app-manager.id}"
  role_name   = "DataSteward"
  crn_pattern = confluent_environment.environment.resource_name
}
