variable "confluent_cloud_api_key" {
  description = "Confluent Cloud API Key"
  type        = string
}

variable "confluent_cloud_api_secret" {
  description = "Confluent Cloud API Secret"
  type        = string
  sensitive   = true
}

variable "confluent_cloud_provider" {
  description = "Confluent Cloud Provider"
  type        = string
}

variable "confluent_cloud_region" {
  description = "Confluent Cloud Region"
  type        = string
}

variable "confluent_cloud_environment_name" {
  description = "Confluent Cloud Environment Name"
  type        = string
}

variable "confluent_cloud_cluster_name" {
  description = "Confluent Cloud Environment Name"
  type        = string
}

variable "confluent_cloud_cluster_type" {
  description = "The type of cluster to be provisioned - Dedicated/Enterprise."
  type        = string
  default     = "enterprise"
}
