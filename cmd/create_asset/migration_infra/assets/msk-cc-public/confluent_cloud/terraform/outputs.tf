output "confluent_cloud_cluster_id" {
  value = confluent_kafka_cluster.cluster.id
}

output "confluent_cloud_cluster_rest_endpoint" {
  value = confluent_kafka_cluster.cluster.rest_endpoint
}

output "confluent_cloud_cluster_api_key" {
  value     = confluent_api_key.app-manager-kafka-api-key.id
  sensitive = true
}

output "confluent_cloud_cluster_api_secret" {
  value     = confluent_api_key.app-manager-kafka-api-key.secret
  sensitive = true
}

output "confluent_cloud_cluster_bootstrap_endpoint" {
  value = confluent_kafka_cluster.cluster.bootstrap_endpoint
}
