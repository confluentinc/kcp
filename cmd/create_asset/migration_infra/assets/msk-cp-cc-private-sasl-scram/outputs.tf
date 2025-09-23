output "confluent_cloud_cluster_rest_endpoint" {
  value = module.confluent_cloud.confluent_cloud_cluster_rest_endpoint
}

output "confluent_cloud_cluster_id" {
  value = module.confluent_cloud.confluent_cloud_cluster_id
}

output "confluent_cloud_cluster_api_key" {
  sensitive = true
  value = module.confluent_cloud_access_setup.confluent_cloud_cluster_key
}

output "confluent_cloud_cluster_api_key_secret" {
  sensitive = true
  value     = module.confluent_cloud_access_setup.confluent_cloud_cluster_secret
}

output "confluent_platform_controller_bootstrap_server" {
  value = module.confluent_platform_broker_instances.confluent_platform_broker_instances_private_dns[0]
}

output "confluent_cloud_cluster_bootstrap_endpoint" {
  value = module.confluent_cloud.confluent_cloud_cluster_bootstrap_endpoint
}
