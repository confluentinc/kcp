variable "vpc_id" {
  description = "The ID of the VPC"
  type        = string
}

variable "security_group_id" {
  description = "The ID of the security group"
  type        = string
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

variable "msk_cluster_id" {
  description = "The ID of the MSK cluster"
  type        = string
}

variable "msk_sasl_scram_username" {
  description = "The SASL SCRAM username of the MSK source cluster"
  type        = string
}

variable "msk_sasl_scram_password" {
  description = "The SASL SCRAM password of the MSK source cluster"
  type        = string
}

variable "msk_cluster_bootstrap_brokers" {
  description = "The bootstrap brokers of the MSK cluster"
  type        = string
}
