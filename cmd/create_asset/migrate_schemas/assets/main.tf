terraform {
  required_providers {
    confluent = {
      source  = "confluentinc/confluent"
      version = "2.50.0"
    }
  }
}

provider "confluent" {
}

resource "confluent_schema_exporter" "api_exporters" {
  for_each = { for req in var.exporters : req.name => req }

  name = each.value.name

  schema_registry_cluster {
    id = var.source_schema_registry_id
  }

  rest_endpoint = var.source_schema_registry_url
  credentials {
    key    = var.source_schema_registry_username
    secret = var.source_schema_registry_password
  }

  subjects     = each.value.subjects
  context_type = each.value.context_type
  context      = each.value.context_name

  destination_schema_registry_cluster {
    rest_endpoint = var.target_schema_registry_url
    credentials {
      key    = var.target_schema_registry_api_key
      secret = var.target_schema_registry_api_secret
    }
  }
}

output "migration_results" {
  description = "Results of all migration requests"
  value = {
    for name, exporter in confluent_schema_exporter.api_exporters : name => {
      exporter_id   = exporter.id
      name = exporter.name
      status        = exporter.status
      subjects      = exporter.subjects
      context_type  = exporter.context_type
      context_name  = exporter.context
    }
  }
}

output "exporter_names" {
  description = "Names of all created schema exporters"
  value       = [for exporter in confluent_schema_exporter.api_exporters : exporter.name]
}

output "exporter_ids" {
  description = "IDs of all created schema exporters"
  value       = [for exporter in confluent_schema_exporter.api_exporters : exporter.id]
}

output "exporter_statuses" {
  description = "Statuses of all schema exporters"
  value       = { for name, exporter in confluent_schema_exporter.api_exporters : name => exporter.status }
}
