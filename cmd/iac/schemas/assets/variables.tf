
variable "confluent_cloud_api_key" {
  description = "Confluent Cloud API Key"
  type        = string
}

variable "confluent_cloud_api_secret" {
  description = "Confluent Cloud API Secret"
  type        = string
  sensitive   = true
}

variable "exporters" {
  description = "List of schema migration requests"
  type = list(object({
    name = string
    context_type  = string
    context_name  = optional(string)
    subjects      = list(string)
  }))
  default = []
}

variable "source_schema_registry_id" {
  description = "ID of the source schema registry cluster"
  type        = string
}

variable "source_schema_registry_url" {
  description = "URL of the source schema registry"
  type        = string
}

variable "source_schema_registry_username" {
  description = "Username for source schema registry authentication"
  type        = string
}

variable "source_schema_registry_password" {
  description = "Password for source schema registry authentication"
  type        = string
  sensitive   = true
}

variable "cc_sr_rest_endpoint" {
  description = "REST endpoint of the Confluent Cloud target schema registry"
  type        = string
}

variable "target_schema_registry_api_key" {
  description = "API key for the Confluent Cloud target schema registry"
  type        = string
}

variable "target_schema_registry_api_secret" {
  description = "API secret for the Confluent Cloud target schema registry"
  type        = string
  sensitive   = true
}
