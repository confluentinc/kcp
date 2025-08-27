output "confluent_cloud_cluster_key" {
  value     = confluent_api_key.app-manager-kafka-api-key.id
  sensitive = true
}

output "confluent_cloud_cluster_secret" {
  value     = confluent_api_key.app-manager-kafka-api-key.secret
  sensitive = true
}
