# Confluent Cloud Access Setup

This module handles the creation of Confluent Cloud API keys, service account, role bindings and ACLs required for beginning the migration.

<!-- BEGIN_TF_DOCS -->
## Requirements

| Name                                                                     | Version |
| ------------------------------------------------------------------------ | ------- |
| <a name="requirement_confluent"></a> [confluent](#requirement_confluent) | 2.23.0  |
| <a name="requirement_random"></a> [random](#requirement_random)          | 3.7.2   |

## Providers

| Name                                                               | Version |
| ------------------------------------------------------------------ | ------- |
| <a name="provider_confluent"></a> [confluent](#provider_confluent) | 2.23.0  |
| <a name="provider_random"></a> [random](#provider_random)          | 3.7.2   |

## Modules

No modules.

## Resources

| Name                                                                                                                                                                 | Type        |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------- |
| [confluent_api_key.app-manager-kafka-api-key](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/api_key)                          | resource    |
| [confluent_api_key.env-manager-schema-registry-api-key](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/api_key)                | resource    |
| [confluent_kafka_acl.app-manager-create-on-cluster](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/kafka_acl)                  | resource    |
| [confluent_kafka_acl.app-manager-describe-on-cluster](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/kafka_acl)                | resource    |
| [confluent_kafka_acl.app-manager-read-all-consumer-groups](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/kafka_acl)           | resource    |
| [confluent_role_binding.app-manager-kafka-cluster-admin](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding)          | resource    |
| [confluent_role_binding.app-manager-kafka-data-steward](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding)           | resource    |
| [confluent_role_binding.subject-resource-owner](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding)                   | resource    |
| [confluent_service_account.app-manager](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/service_account)                        | resource    |
| [random_string.suffix](https://registry.terraform.io/providers/hashicorp/random/3.7.2/docs/resources/string)                                                         | resource    |
| [confluent_environment.environment](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/data-sources/environment)                             | data source |
| [confluent_kafka_cluster.cluster](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/data-sources/kafka_cluster)                             | data source |
| [confluent_schema_registry_cluster.schema_registry](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/data-sources/schema_registry_cluster) | data source |

## Inputs

| Name                                                                                                                        | Description                    | Type     | Default | Required |
| --------------------------------------------------------------------------------------------------------------------------- | ------------------------------ | -------- | ------- | :------: |
| <a name="input_confluent_cloud_cluster_id"></a> [confluent_cloud_cluster_id](#input_confluent_cloud_cluster_id)             | Confluent Cloud Cluster ID     | `string` | n/a     |   yes    |
| <a name="input_confluent_cloud_environment_id"></a> [confluent_cloud_environment_id](#input_confluent_cloud_environment_id) | Confluent Cloud Environment ID | `string` | n/a     |   yes    |

## Outputs

| Name                                                                                                                          | Description |
| ----------------------------------------------------------------------------------------------------------------------------- | ----------- |
| <a name="output_confluent_cloud_cluster_key"></a> [confluent_cloud_cluster_key](#output_confluent_cloud_cluster_key)          | n/a         |
| <a name="output_confluent_cloud_cluster_secret"></a> [confluent_cloud_cluster_secret](#output_confluent_cloud_cluster_secret) | n/a         |
<!-- END_TF_DOCS -->
