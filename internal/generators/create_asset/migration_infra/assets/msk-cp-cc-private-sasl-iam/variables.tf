variable "confluent_cloud_provider" {
  type      = string
  sensitive = false
}

variable "confluent_cloud_region" {
  type      = string
  sensitive = false
}

variable "confluent_cloud_environment_name" {
  type      = string
  sensitive = false
}

variable "confluent_cloud_cluster_name" {
  type      = string
  sensitive = false
}

variable "confluent_cloud_cluster_type" {
  type      = string
  sensitive = false
}

variable "confluent_cloud_api_key" {
  type      = string
  sensitive = true
}

variable "confluent_cloud_api_secret" {
  type      = string
  sensitive = true
}

variable "aws_region" {
  type      = string
  sensitive = false
}

variable "customer_vpc_id" {
  type      = string
  sensitive = false
}

variable "aws_zones" {
  type      = list(object({ zone = string, cidr = string }))
  sensitive = false
}

variable "msk_cluster_id" {
  type      = string
  sensitive = false
}

variable "msk_cluster_bootstrap_brokers" {
  description = "The bootstrap brokers of the MSK cluster"
  type        = string
}

variable "msk_cluster_arn" {
  description = "The ARN of the MSK cluster"
}

variable "ansible_control_node_subnet_cidr" {
  description = "The CIDR block of the ansible instance subnet"
  type        = string
}

variable "confluent_platform_broker_iam_role_name" {
  description = "The name of the existing IAM role to attach to the Confluent Platform broker instances"
  type        = string
}
