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

variable "ansible_control_node_subnet_cidr" {
  description = "The CIDR block of the ansible instance subnet"
  type        = string
}

variable "aws_security_group_ids" {
  description = "Comma separated AWS Security Group Ids"
  type        = string
}
