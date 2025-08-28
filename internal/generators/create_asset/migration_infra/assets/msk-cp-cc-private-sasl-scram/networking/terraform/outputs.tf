output "confluent_platform_broker_instances_security_group_id" {
  value = var.aws_security_group_ids != "" ? var.aws_security_group_ids : aws_security_group.confluent_platform_broker_instances_security_group[0].id
}

output "private_link_security_group_id" {
  value = var.aws_security_group_ids != "" ? var.aws_security_group_ids : aws_security_group.private_link_security_group[0].id
}

output "public_subnet_id" {
  value = aws_subnet.ansible_control_node_instance_public_subnet.id
}

output "confluent_platform_broker_subnet_ids" {
  value = values(aws_subnet.confluent_platform_broker_subnet_ids)[*].id
}

output "aws_key_pair_name" {
  value = aws_key_pair.ansible_confluent_platform_broker_ssh_key.key_name
}

output "private_key" {
  value = tls_private_key.ansible_confluent_platform_broker_ssh_key.private_key_pem
}
