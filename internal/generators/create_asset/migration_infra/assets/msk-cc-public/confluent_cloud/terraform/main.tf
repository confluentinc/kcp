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

resource "confluent_kafka_cluster" "cluster" {
  display_name = var.confluent_cloud_cluster_name
  availability = "SINGLE_ZONE"
  cloud        = var.confluent_cloud_provider
  region       = var.confluent_cloud_region

  dedicated {
    cku = 1
  }

  environment {
    id = confluent_environment.environment.id
  }
}

data "confluent_schema_registry_cluster" "schema_registry" {
  environment {
    id = confluent_environment.environment.id
  }

  depends_on = [confluent_api_key.app-manager-kafka-api-key]
}

resource "confluent_api_key" "env-manager-schema-registry-api-key" {
  display_name = "env-manager-schema-registry-api-key-${random_string.suffix.result}"
  description  = "Schema Registry API Key that is owned by the ${var.confluent_cloud_environment_name} environment."

  owner {
    id          = confluent_service_account.app-manager.id
    api_version = confluent_service_account.app-manager.api_version
    kind        = confluent_service_account.app-manager.kind
  }

  managed_resource {
    id          = data.confluent_schema_registry_cluster.schema_registry.id
    api_version = data.confluent_schema_registry_cluster.schema_registry.api_version
    kind        = data.confluent_schema_registry_cluster.schema_registry.kind

    environment {
      id = confluent_environment.environment.id
    }
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

resource "confluent_kafka_acl" "app-manager-create-on-cluster" {
  kafka_cluster {
    id = confluent_kafka_cluster.cluster.id
  }

  resource_type = "CLUSTER"
  resource_name = "kafka-cluster"
  pattern_type  = "LITERAL"
  principal     = "User:${confluent_service_account.app-manager.id}"
  host          = "*"
  operation     = "CREATE"
  permission    = "ALLOW"
  rest_endpoint = confluent_kafka_cluster.cluster.rest_endpoint

  credentials {
    key    = confluent_api_key.app-manager-kafka-api-key.id
    secret = confluent_api_key.app-manager-kafka-api-key.secret
  }
}

resource "confluent_kafka_acl" "app-manager-describe-on-cluster" {
  kafka_cluster {
    id = confluent_kafka_cluster.cluster.id
  }

  resource_type = "CLUSTER"
  resource_name = "kafka-cluster"
  pattern_type  = "LITERAL"
  principal     = "User:${confluent_service_account.app-manager.id}"
  host          = "*"
  operation     = "DESCRIBE"
  permission    = "ALLOW"
  rest_endpoint = confluent_kafka_cluster.cluster.rest_endpoint

  credentials {
    key    = confluent_api_key.app-manager-kafka-api-key.id
    secret = confluent_api_key.app-manager-kafka-api-key.secret
  }
}

resource "confluent_kafka_acl" "app-manager-read-all-consumer-groups" {
  kafka_cluster {
    id = confluent_kafka_cluster.cluster.id
  }

  resource_type = "GROUP"
  resource_name = "*"
  pattern_type  = "PREFIXED"
  principal     = "User:${confluent_service_account.app-manager.id}"
  host          = "*"
  operation     = "READ"
  permission    = "ALLOW"
  rest_endpoint = confluent_kafka_cluster.cluster.rest_endpoint

  credentials {
    key    = confluent_api_key.app-manager-kafka-api-key.id
    secret = confluent_api_key.app-manager-kafka-api-key.secret
  }
}

resource "confluent_api_key" "app-manager-kafka-api-key" {
  display_name = "app-manager-kafka-api-key-${random_string.suffix.result}"
  description  = "Kafka API Key that has been created by the ${var.confluent_cloud_environment_name} environment."
  owner {
    id          = confluent_service_account.app-manager.id
    api_version = confluent_service_account.app-manager.api_version
    kind        = confluent_service_account.app-manager.kind
  }

  managed_resource {
    id          = confluent_kafka_cluster.cluster.id
    api_version = confluent_kafka_cluster.cluster.api_version
    kind        = confluent_kafka_cluster.cluster.kind

    environment {
      id = confluent_environment.environment.id
    }
  }

  depends_on = [
    confluent_role_binding.app-manager-kafka-cluster-admin
  ]
}
