# Confluent Cloud Private Link Connection

This module creates a private link connection between AWS and Confluent Cloud, enabling secure and private communication between your AWS VPC and Confluent Cloud services.

<!-- BEGIN_TF_DOCS -->
## Requirements

| Name | Version |
|------|---------|
| <a name="requirement_aws"></a> [aws](#requirement\_aws) | 5.80.0 |
| <a name="requirement_confluent"></a> [confluent](#requirement\_confluent) | 2.23.0 |
| <a name="requirement_random"></a> [random](#requirement\_random) | 3.7.2 |
| <a name="requirement_time"></a> [time](#requirement\_time) | 0.13.1-alpha1 |

## Providers

| Name | Version |
|------|---------|
| <a name="provider_aws"></a> [aws](#provider\_aws) | 5.80.0 |
| <a name="provider_confluent"></a> [confluent](#provider\_confluent) | 2.23.0 |
| <a name="provider_random"></a> [random](#provider\_random) | 3.7.2 |
| <a name="provider_time"></a> [time](#provider\_time) | 0.13.1-alpha1 |

## Modules

No modules.

## Resources

| Name | Type |
|------|------|
| [aws_route53_record.entries](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/route53_record) | resource |
| [aws_route53_zone.private](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/route53_zone) | resource |
| [aws_vpc_endpoint.main](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/vpc_endpoint) | resource |
| [confluent_private_link_attachment.aws](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/private_link_attachment) | resource |
| [confluent_private_link_attachment_connection.aws](https://registry.terraform.io/providers/confluentinc/confluent/2.23.0/docs/resources/private_link_attachment_connection) | resource |
| [random_string.suffix](https://registry.terraform.io/providers/hashicorp/random/3.7.2/docs/resources/string) | resource |
| [time_sleep.destroy_delay](https://registry.terraform.io/providers/hashicorp/time/0.13.1-alpha1/docs/resources/sleep) | resource |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| <a name="input_aws_access_key"></a> [aws\_access\_key](#input\_aws\_access\_key) | IAM access key used to authenticate with AWS. | `string` | n/a | yes |
| <a name="input_aws_access_secret"></a> [aws\_access\_secret](#input\_aws\_access\_secret) | IAM access secret used to authenticate with AWS. | `string` | n/a | yes |
| <a name="input_aws_credentials_profile"></a> [aws\_credentials\_profile](#input\_aws\_credentials\_profile) | Profile to use from the ~/.aws/credentials file. | `string` | n/a | yes |
| <a name="input_aws_region"></a> [aws\_region](#input\_aws\_region) | The AWS region | `string` | n/a | yes |
| <a name="input_confluent_cloud_api_key"></a> [confluent\_cloud\_api\_key](#input\_confluent\_cloud\_api\_key) | Confluent Cloud API Key | `string` | n/a | yes |
| <a name="input_confluent_cloud_api_secret"></a> [confluent\_cloud\_api\_secret](#input\_confluent\_cloud\_api\_secret) | Confluent Cloud API Secret | `string` | n/a | yes |
| <a name="input_confluent_cloud_environment_id"></a> [confluent\_cloud\_environment\_id](#input\_confluent\_cloud\_environment\_id) | The ID of the Confluent environment | `string` | n/a | yes |
| <a name="input_security_group_id"></a> [security\_group\_id](#input\_security\_group\_id) | The ID of the security group for the VPC endpoint | `string` | n/a | yes |
| <a name="input_subnet_ids"></a> [subnet\_ids](#input\_subnet\_ids) | List of subnet IDs for the VPC endpoint | `list(string)` | n/a | yes |
| <a name="input_vpc_id"></a> [vpc\_id](#input\_vpc\_id) | The ID of the VPC | `string` | n/a | yes |

## Outputs

| Name | Description |
|------|-------------|
| <a name="output_private_link_attachment_id"></a> [private\_link\_attachment\_id](#output\_private\_link\_attachment\_id) | The ID of the private link attachment |
| <a name="output_private_link_connection_id"></a> [private\_link\_connection\_id](#output\_private\_link\_connection\_id) | The ID of the private link connection |
| <a name="output_vpc_endpoint_id"></a> [vpc\_endpoint\_id](#output\_vpc\_endpoint\_id) | The ID of the VPC endpoint |
<!-- END_TF_DOCS -->
