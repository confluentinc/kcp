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

variable "confluent_cloud_api_key" {
  type      = string
  sensitive = true
}

variable "confluent_cloud_api_secret" {
  type      = string
  sensitive = true
}

variable "msk_cluster_id" {
  type      = string
  sensitive = false
}

variable "msk_cluster_bootstrap_brokers" {
  description = "The bootstrap brokers of the MSK cluster"
  type        = string
}

variable "msk_sasl_scram_username" {
  type      = string
}

variable "msk_sasl_scram_password" {
  type      = string
  sensitive = true
}