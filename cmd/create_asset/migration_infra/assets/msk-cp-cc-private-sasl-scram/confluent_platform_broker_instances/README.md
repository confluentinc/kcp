# Confluent Platform Brokers

This module creates some EC2 instances that will host the Confluent Platform brokers used for the jump cluster.

<!-- BEGIN_TF_DOCS -->
## Requirements

| Name | Version |
|------|---------|
| <a name="requirement_aws"></a> [aws](#requirement\_aws) | 5.80.0 |
| <a name="requirement_random"></a> [random](#requirement\_random) | 3.7.2 |

## Providers

| Name | Version |
|------|---------|
| <a name="provider_aws"></a> [aws](#provider\_aws) | 5.80.0 |

## Modules

No modules.

## Resources

| Name | Type |
|------|------|
| [aws_instance.confluent-platform-broker](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/instance) | resource |
| [aws_ami.red_hat_linux_ami](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/data-sources/ami) | data source |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| <a name="input_aws_key_pair_name"></a> [aws\_key\_pair\_name](#input\_aws\_key\_pair\_name) | The name of the key pair | `string` | n/a | yes |
| <a name="input_aws_public_subnet_id"></a> [aws\_public\_subnet\_id](#input\_aws\_public\_subnet\_id) | The ID of the public subnet | `string` | n/a | yes |
| <a name="input_aws_region"></a> [aws\_region](#input\_aws\_region) | The AWS region | `string` | n/a | yes |
| <a name="input_confluent_cloud_cluster_bootstrap_endpoint"></a> [confluent\_cloud\_cluster\_bootstrap\_endpoint](#input\_confluent\_cloud\_cluster\_bootstrap\_endpoint) | The bootstrap endpoint of the Confluent Cloud cluster | `string` | n/a | yes |
| <a name="input_confluent_cloud_cluster_id"></a> [confluent\_cloud\_cluster\_id](#input\_confluent\_cloud\_cluster\_id) | The ID of the Confluent Cloud cluster | `string` | n/a | yes |
| <a name="input_confluent_cloud_cluster_key"></a> [confluent\_cloud\_cluster\_key](#input\_confluent\_cloud\_cluster\_key) | The key of the Confluent Cloud cluster | `string` | n/a | yes |
| <a name="input_confluent_cloud_cluster_rest_endpoint"></a> [confluent\_cloud\_cluster\_rest\_endpoint](#input\_confluent\_cloud\_cluster\_rest\_endpoint) | The REST endpoint of the Confluent Cloud cluster | `string` | n/a | yes |
| <a name="input_confluent_cloud_cluster_secret"></a> [confluent\_cloud\_cluster\_secret](#input\_confluent\_cloud\_cluster\_secret) | The secret of the Confluent Cloud cluster | `string` | n/a | yes |
| <a name="input_confluent_platform_broker_iam_role_name"></a> [confluent\_platform\_broker\_iam\_role\_name](#input\_confluent\_platform\_broker\_iam\_role\_name) | The name of the existing IAM role to attach to the Confluent Platform broker instances | `string` | n/a | yes |
| <a name="input_confluent_platform_broker_subnet_ids"></a> [confluent\_platform\_broker\_subnet\_ids](#input\_confluent\_platform\_broker\_subnet\_ids) | List of subnet IDs for the VPC endpoint | `list(string)` | n/a | yes |
| <a name="input_msk_cluster_arn"></a> [msk\_cluster\_arn](#input\_msk\_cluster\_arn) | The ARN of the MSK cluster | `string` | n/a | yes |
| <a name="input_msk_cluster_bootstrap_brokers"></a> [msk\_cluster\_bootstrap\_brokers](#input\_msk\_cluster\_bootstrap\_brokers) | The bootstrap brokers of the MSK cluster | `string` | n/a | yes |
| <a name="input_msk_cluster_id"></a> [msk\_cluster\_id](#input\_msk\_cluster\_id) | The ID of the MSK cluster | `string` | n/a | yes |
| <a name="input_private_key"></a> [private\_key](#input\_private\_key) | The private key | `string` | n/a | yes |
| <a name="input_security_group_id"></a> [security\_group\_id](#input\_security\_group\_id) | The ID of the security group | `string` | n/a | yes |
| <a name="input_vpc_id"></a> [vpc\_id](#input\_vpc\_id) | The ID of the VPC | `string` | n/a | yes |

## Outputs

| Name | Description |
|------|-------------|
| <a name="output_confluent_platform_broker_instances_private_dns"></a> [confluent\_platform\_broker\_instances\_private\_dns](#output\_confluent\_platform\_broker\_instances\_private\_dns) | n/a |
<!-- END_TF_DOCS -->
