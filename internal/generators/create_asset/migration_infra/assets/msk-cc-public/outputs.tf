output "confluent_cloud_cluster_rest_endpoint" {
  value = module.confluent_cloud.confluent_cloud_cluster_rest_endpoint
}

output "confluent_cloud_cluster_id" {
  value = module.confluent_cloud.confluent_cloud_cluster_id
}

output "confluent_cloud_cluster_api_key" {
  value = module.confluent_cloud.confluent_cloud_cluster_api_key
}

output "confluent_cloud_cluster_api_key_secret" {
  sensitive = true
  value     = module.confluent_cloud.confluent_cloud_cluster_api_secret
}

output "confluent_cloud_cluster_bootstrap_endpoint" {
  value = module.confluent_cloud.confluent_cloud_cluster_bootstrap_endpoint
}
