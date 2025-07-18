# Confluent Cloud

This module creates a Confluent Cloud Environment and Cluster.

<!-- BEGIN_TF_DOCS -->
## Requirements

| Name | Version |
|------|---------|
| <a name="requirement_confluent"></a> [confluent](#requirement\_confluent) | 2.23.0 |
| <a name="requirement_random"></a> [random](#requirement\_random) | 3.7.2 |

## Providers

| Name | Version |
|------|---------|
| <a name="provider_confluent"></a> [confluent](#provider\_confluent) | 2.23.0 |
| <a name="provider_random"></a> [random](#provider\_random) | 3.7.2 |

## Modules

No modules.

## Resources

| Name | Type |
|------|------|
| [confluent_api_key.app-manager-flink-api-key](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/api_key) | resource |
| [confluent_api_key.app-manager-kafka-api-key](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/api_key) | resource |
| [confluent_api_key.app-manager-tableflow-api-key](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/api_key) | resource |
| [confluent_api_key.env-manager-schema-registry-api-key](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/api_key) | resource |
| [confluent_environment.environment](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/environment) | resource |
| [confluent_kafka_acl.app-manager-create-on-cluster](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/kafka_acl) | resource |
| [confluent_kafka_acl.app-manager-describe-on-cluster](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/kafka_acl) | resource |
| [confluent_kafka_acl.app-manager-read-all-consumer-groups](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/kafka_acl) | resource |
| [confluent_kafka_cluster.cluster](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/kafka_cluster) | resource |
| [confluent_role_binding.app-manager-assigner](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding) | resource |
| [confluent_role_binding.app-manager-flink-developer](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding) | resource |
| [confluent_role_binding.app-manager-flink-kafka-env-admin](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding) | resource |
| [confluent_role_binding.app-manager-kafka-cluster-admin](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding) | resource |
| [confluent_role_binding.app-manager-kafka-data-steward](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding) | resource |
| [confluent_role_binding.subject-resource-owner](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/role_binding) | resource |
| [confluent_service_account.app-manager](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/service_account) | resource |
| [random_string.suffix](https://registry.terraform.io/providers/hashicorp/random/3.7.2/docs/resources/string) | resource |
| [confluent_flink_region.example](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/data-sources/flink_region) | data source |
| [confluent_organization.main](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/data-sources/organization) | data source |
| [confluent_schema_registry_cluster.terrasnap_schema_registry](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/data-sources/schema_registry_cluster) | data source |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| <a name="input_confluent_cloud_api_key"></a> [confluent\_cloud\_api\_key](#input\_confluent\_cloud\_api\_key) | Confluent Cloud API Key | `string` | n/a | yes |
| <a name="input_confluent_cloud_api_secret"></a> [confluent\_cloud\_api\_secret](#input\_confluent\_cloud\_api\_secret) | Confluent Cloud API Secret | `string` | n/a | yes |
| <a name="input_confluent_cloud_cluster_name"></a> [confluent\_cloud\_cluster\_name](#input\_confluent\_cloud\_cluster\_name) | Confluent Cloud Environment Name | `string` | n/a | yes |
| <a name="input_confluent_cloud_cluster_type"></a> [confluent\_cloud\_cluster\_type](#input\_confluent\_cloud\_cluster\_type) | The type of cluster to be provisioned - Basic/Standard/Dedicated. | `string` | n/a | yes |
| <a name="input_confluent_cloud_environment_name"></a> [confluent\_cloud\_environment\_name](#input\_confluent\_cloud\_environment\_name) | Confluent Cloud Environment Name | `string` | n/a | yes |
| <a name="input_confluent_cloud_provider"></a> [confluent\_cloud\_provider](#input\_confluent\_cloud\_provider) | Confluent Cloud Provider | `string` | n/a | yes |
| <a name="input_confluent_cloud_region"></a> [confluent\_cloud\_region](#input\_confluent\_cloud\_region) | Confluent Cloud Region | `string` | n/a | yes |

## Outputs

| Name | Description |
|------|-------------|
| <a name="output_confluent_cloud_cluster_bootstrap_endpoint"></a> [confluent\_cloud\_cluster\_bootstrap\_endpoint](#output\_confluent\_cloud\_cluster\_bootstrap\_endpoint) | n/a |
| <a name="output_confluent_cloud_cluster_id"></a> [confluent\_cloud\_cluster\_id](#output\_confluent\_cloud\_cluster\_id) | n/a |
| <a name="output_confluent_cloud_cluster_key"></a> [confluent\_cloud\_cluster\_key](#output\_confluent\_cloud\_cluster\_key) | n/a |
| <a name="output_confluent_cloud_cluster_rest_endpoint"></a> [confluent\_cloud\_cluster\_rest\_endpoint](#output\_confluent\_cloud\_cluster\_rest\_endpoint) | n/a |
| <a name="output_confluent_cloud_cluster_secret"></a> [confluent\_cloud\_cluster\_secret](#output\_confluent\_cloud\_cluster\_secret) | n/a |
| <a name="output_confluent_cloud_environment_flink_api_key"></a> [confluent\_cloud\_environment\_flink\_api\_key](#output\_confluent\_cloud\_environment\_flink\_api\_key) | n/a |
| <a name="output_confluent_cloud_environment_flink_api_secret"></a> [confluent\_cloud\_environment\_flink\_api\_secret](#output\_confluent\_cloud\_environment\_flink\_api\_secret) | n/a |
| <a name="output_confluent_cloud_environment_id"></a> [confluent\_cloud\_environment\_id](#output\_confluent\_cloud\_environment\_id) | n/a |
| <a name="output_confluent_cloud_environment_tableflow_api_key"></a> [confluent\_cloud\_environment\_tableflow\_api\_key](#output\_confluent\_cloud\_environment\_tableflow\_api\_key) | n/a |
| <a name="output_confluent_cloud_environment_tableflow_api_secret"></a> [confluent\_cloud\_environment\_tableflow\_api\_secret](#output\_confluent\_cloud\_environment\_tableflow\_api\_secret) | n/a |
| <a name="output_confluent_cloud_schema_registry_api_key"></a> [confluent\_cloud\_schema\_registry\_api\_key](#output\_confluent\_cloud\_schema\_registry\_api\_key) | n/a |
| <a name="output_confluent_cloud_schema_registry_api_secret"></a> [confluent\_cloud\_schema\_registry\_api\_secret](#output\_confluent\_cloud\_schema\_registry\_api\_secret) | n/a |
| <a name="output_confluent_cloud_schema_registry_id"></a> [confluent\_cloud\_schema\_registry\_id](#output\_confluent\_cloud\_schema\_registry\_id) | n/a |
| <a name="output_confluent_cloud_schema_registry_rest_endpoint"></a> [confluent\_cloud\_schema\_registry\_rest\_endpoint](#output\_confluent\_cloud\_schema\_registry\_rest\_endpoint) | n/a |
| <a name="output_confluent_cloud_service_account_id"></a> [confluent\_cloud\_service\_account\_id](#output\_confluent\_cloud\_service\_account\_id) | n/a |
<!-- END_TF_DOCS -->
