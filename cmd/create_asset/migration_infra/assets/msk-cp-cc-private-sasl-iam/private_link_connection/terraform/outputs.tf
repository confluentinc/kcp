output "private_link_attachment_id" {
  description = "The ID of the private link attachment"
  value       = confluent_private_link_attachment.aws.id
}

output "vpc_endpoint_id" {
  description = "The ID of the VPC endpoint"
  value       = aws_vpc_endpoint.main.id
}

output "private_link_connection_id" {
  description = "The ID of the private link connection"
  value       = confluent_private_link_attachment_connection.aws.id
}
