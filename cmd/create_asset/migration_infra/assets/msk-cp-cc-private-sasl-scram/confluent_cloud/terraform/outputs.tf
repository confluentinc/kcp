output "confluent_cloud_environment_id" {
  value = confluent_environment.environment.id
}

output "confluent_cloud_cluster_id" {
  value = confluent_kafka_cluster.cluster.id
}

output "confluent_cloud_cluster_bootstrap_endpoint" {
  value = confluent_kafka_cluster.cluster.bootstrap_endpoint
}

output "confluent_cloud_cluster_rest_endpoint" {
  value = confluent_kafka_cluster.cluster.rest_endpoint
}
