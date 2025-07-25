variable "aws_region" {
  description = "The AWS region"
  type        = string
}

variable "confluent_cloud_environment_id" {
  description = "The ID of the Confluent environment"
  type        = string
}

variable "vpc_id" {
  description = "The ID of the VPC"
  type        = string
}

variable "confluent_platform_broker_subnet_ids" {
  description = "List of subnet IDs for the VPC endpoint"
  type        = list(string)
}

variable "security_group_id" {
  description = "The ID of the security group used to connect to the VPC endpoint and for the reverse proxy."
  type        = string
}
