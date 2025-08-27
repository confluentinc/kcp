resource "random_string" "suffix" {
  length  = 4
  special = false
  numeric = false
  upper   = false
}

data "confluent_environment" "environment" {
  id = var.confluent_cloud_environment_id
}

data "confluent_kafka_cluster" "cluster" {
  id = var.confluent_cloud_cluster_id

  environment {
    id = data.confluent_environment.environment.id
  }
}

data "confluent_service_account" "app-manager" {
  id = var.confluent_cloud_service_account_id
}

resource "confluent_kafka_acl" "app-manager-create-on-cluster" {
  kafka_cluster {
    id = data.confluent_kafka_cluster.cluster.id
  }

  resource_type = "CLUSTER"
  resource_name = "kafka-cluster"
  pattern_type  = "LITERAL"
  principal     = "User:${data.confluent_service_account.app-manager.id}"
  host          = "*"
  operation     = "CREATE"
  permission    = "ALLOW"
  rest_endpoint = data.confluent_kafka_cluster.cluster.rest_endpoint

  credentials {
    key    = confluent_api_key.app-manager-kafka-api-key.id
    secret = confluent_api_key.app-manager-kafka-api-key.secret
  }
}

resource "confluent_kafka_acl" "app-manager-describe-on-cluster" {
  kafka_cluster {
    id = data.confluent_kafka_cluster.cluster.id
  }

  resource_type = "CLUSTER"
  resource_name = "kafka-cluster"
  pattern_type  = "LITERAL"
  principal     = "User:${data.confluent_service_account.app-manager.id}"
  host          = "*"
  operation     = "DESCRIBE"
  permission    = "ALLOW"
  rest_endpoint = data.confluent_kafka_cluster.cluster.rest_endpoint

  credentials {
    key    = confluent_api_key.app-manager-kafka-api-key.id
    secret = confluent_api_key.app-manager-kafka-api-key.secret
  }
}

resource "confluent_kafka_acl" "app-manager-read-all-consumer-groups" {
  kafka_cluster {
    id = data.confluent_kafka_cluster.cluster.id
  }

  resource_type = "GROUP"
  resource_name = "*"
  pattern_type  = "PREFIXED"
  principal     = "User:${data.confluent_service_account.app-manager.id}"
  host          = "*"
  operation     = "READ"
  permission    = "ALLOW"
  rest_endpoint = data.confluent_kafka_cluster.cluster.rest_endpoint

  credentials {
    key    = confluent_api_key.app-manager-kafka-api-key.id
    secret = confluent_api_key.app-manager-kafka-api-key.secret
  }
}

resource "confluent_api_key" "app-manager-kafka-api-key" {
  display_name = "app-manager-kafka-api-key-${random_string.suffix.result}"
  description  = "Kafka API Key that has been created as part of the kcp migration."

  owner {
    id          = data.confluent_service_account.app-manager.id
    api_version = data.confluent_service_account.app-manager.api_version
    kind        = data.confluent_service_account.app-manager.kind
  }

  managed_resource {
    id          = data.confluent_kafka_cluster.cluster.id
    api_version = data.confluent_kafka_cluster.cluster.api_version
    kind        = data.confluent_kafka_cluster.cluster.kind

    environment {
      id = data.confluent_environment.environment.id
    }
  }
}

data "confluent_schema_registry_cluster" "schema_registry" {
  environment {
    id = data.confluent_environment.environment.id
  }

  depends_on = [confluent_api_key.app-manager-kafka-api-key]
}

resource "confluent_api_key" "env-manager-schema-registry-api-key" {
  display_name = "env-manager-schema-registry-api-key-${random_string.suffix.result}"
  description  = "Schema Registry API Key that has been created as part of the kcp migration."

  owner {
    id          = data.confluent_service_account.app-manager.id
    api_version = data.confluent_service_account.app-manager.api_version
    kind        = data.confluent_service_account.app-manager.kind
  }

  managed_resource {
    id          = data.confluent_schema_registry_cluster.schema_registry.id
    api_version = data.confluent_schema_registry_cluster.schema_registry.api_version
    kind        = data.confluent_schema_registry_cluster.schema_registry.kind

    environment {
      id = data.confluent_environment.environment.id
    }
  }
}
