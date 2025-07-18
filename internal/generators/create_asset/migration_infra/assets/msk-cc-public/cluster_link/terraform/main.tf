locals {
  link_name              = "msk-to-cc-link"
  basic_auth_credentials = base64encode("${var.confluent_cloud_cluster_api_key}:${var.confluent_cloud_cluster_api_secret}")
}

resource "null_resource" "confluent_cluster_link" {
  triggers = {
    source_cluster_id      = var.msk_cluster_id
    destination_cluster_id = var.confluent_cloud_cluster_id
    bootstrap_servers      = var.msk_bootstrap_servers
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl --request POST \
        --url 'https://${var.confluent_cloud_cluster_rest_endpoint}/kafka/v3/clusters/${var.confluent_cloud_cluster_id}/links/?link_name=${local.link_name}' \
        --header 'Authorization: Basic ${local.basic_auth_credentials}' \
        --header "Content-Type: application/json" \
        --data '{
          "source_cluster_id": "${var.msk_cluster_id}",
          "configs": [
            {
              "name": "bootstrap.servers",
              "value": "${var.msk_bootstrap_servers}"
            },
            {
              "name": "link.mode",
              "value": "DESTINATION"
            },
            {
              "name": "security.protocol",
              "value": "SASL_SSL"
            },
            {
              "name": "sasl.mechanism",
              "value": "SCRAM-SHA-512"
            },
            {
              "name": "sasl.jaas.config",
              "value": "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"${var.msk_sasl_scram_username}\" password=\"${var.msk_sasl_scram_password}\";"
            }
          ]
        }'
    EOT
  }
}
