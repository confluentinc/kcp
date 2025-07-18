variable "confluent_cloud_cluster_id" {
  description = "The Confluent Cloud cluster ID."
  type        = string
}

variable "confluent_cloud_cluster_api_key" {
  description = "The Confluent Cloud cluster API key."
  type        = string
}

variable "confluent_cloud_cluster_api_secret" {
  description = "The Confluent Cloud cluster API secret."
  type        = string
  sensitive   = true
}

variable "confluent_cloud_cluster_rest_endpoint" {
  description = "The Confluent Cloud cluster REST endpoint."
  type        = string
}

variable "msk_cluster_id" {
  description = "The Kafka cluster ID of the MSK cluster."
  type        = string
}

variable "msk_bootstrap_servers" {
  description = "The MSK public bootstrap servers used to connect to the MSK cluster."
  type        = string
}

variable "msk_sasl_scram_username" {
  description = "The MSK SASL SCRAM username used to authenticate the cluster link."
  type        = string
}

variable "msk_sasl_scram_password" {
  description = "The MSK SASL SCRAM password used to authenticate the cluster link."
  type        = string
  sensitive   = true
}
