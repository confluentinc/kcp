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

variable "msk_cluster_id" {
  description = "The ID of the MSK cluster"
  type        = string
}

variable "msk_cluster_bootstrap_brokers" {
  description = "The bootstrap brokers of the MSK cluster"
  type        = string
}

variable "msk_cluster_arn" {
  description = "The ARN of the MSK cluster"
  type        = string
}

variable "aws_region" {
  description = "The AWS region"
  type        = string
}

variable "confluent_platform_broker_iam_role_name" {
  description = "The name of the existing IAM role to attach to the Confluent Platform broker instances"
  type        = string
}
