variable "vpc_id" {
  description = "The ID of the VPC"
  type        = string
}

variable "security_group_id" {
  description = "List of string of AWS Security Group Ids"
  type        = list(string)
}

variable "aws_public_subnet_id" {
  description = "The ID of the public subnet"
  type        = string
}

variable "aws_key_pair_name" {
  description = "The name of the key pair"
  type        = string
}

variable "private_key" {
  description = "The private key"
  type        = string
  sensitive   = true
}

variable "confluent_platform_broker_subnet_ids" {
  description = "List of subnet IDs for the VPC endpoint"
  type        = list(string)
}

variable "confluent_cloud_cluster_rest_endpoint" {
  description = "The REST endpoint of the Confluent Cloud cluster"
  type        = string
}

variable "confluent_cloud_cluster_id" {
  description = "The ID of the Confluent Cloud cluster"
  type        = string
}

variable "confluent_cloud_cluster_key" {
  description = "The key of the Confluent Cloud cluster"
  type        = string
  sensitive   = true
}

variable "confluent_cloud_cluster_secret" {
  description = "The secret of the Confluent Cloud cluster"
  type        = string
  sensitive   = true
}

variable "confluent_cloud_cluster_bootstrap_endpoint" {
  description = "The bootstrap endpoint of the Confluent Cloud cluster"
  type        = string
}

variable "confluent_platform_broker_instances_private_dns" {
  description = "The private DNS of the Confluent Cloud broker instances"
  type        = list(string)
}
