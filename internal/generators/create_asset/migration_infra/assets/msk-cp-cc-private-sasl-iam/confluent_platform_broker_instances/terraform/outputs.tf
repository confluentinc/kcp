output "confluent_platform_broker_instances_private_dns" {
  value = values(aws_instance.confluent-platform-broker)[*].private_dns
}
