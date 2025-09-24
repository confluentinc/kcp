# Networking

This module creates all the networking components required for deploying a jump cluster to migrate data from AWS MSK to Confluent Cloud using cluster linking in a private network.

<!-- BEGIN_TF_DOCS -->
## Requirements

| Name | Version |
|------|---------|
| <a name="requirement_aws"></a> [aws](#requirement\_aws) | 5.80.0 |
| <a name="requirement_external"></a> [external](#requirement\_external) | 2.3.4 |
| <a name="requirement_local"></a> [local](#requirement\_local) | 2.4.0 |
| <a name="requirement_random"></a> [random](#requirement\_random) | 3.7.2 |
| <a name="requirement_tls"></a> [tls](#requirement\_tls) | 4.0.6 |

## Providers

| Name | Version |
|------|---------|
| <a name="provider_aws"></a> [aws](#provider\_aws) | 5.80.0 |
| <a name="provider_external"></a> [external](#provider\_external) | 2.3.4 |
| <a name="provider_local"></a> [local](#provider\_local) | 2.4.0 |
| <a name="provider_random"></a> [random](#provider\_random) | 3.7.2 |
| <a name="provider_tls"></a> [tls](#provider\_tls) | 4.0.6 |

## Modules

No modules.

## Resources

| Name | Type |
|------|------|
| [aws_eip.nat_eip](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/eip) | resource |
| [aws_key_pair.ansible_confluent_platform_broker_ssh_key](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/key_pair) | resource |
| [aws_nat_gateway.nat_gw](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/nat_gateway) | resource |
| [aws_route_table.ansible_control_node_instance_public_rt](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/route_table) | resource |
| [aws_route_table.private_subnet_rt](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/route_table) | resource |
| [aws_route_table_association.ansible_control_node_instance_public_rt_association](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/route_table_association) | resource |
| [aws_route_table_association.private_rt_association](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/route_table_association) | resource |
| [aws_security_group.confluent_platform_broker_instances_security_group](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/security_group) | resource |
| [aws_security_group.private_link_security_group](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/security_group) | resource |
| [aws_subnet.ansible_control_node_instance_public_subnet](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/subnet) | resource |
| [aws_subnet.confluent_platform_broker_subnet_ids](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/resources/subnet) | resource |
| [local_file.ansible_confluent_platform_broker_private_key](https://registry.terraform.io/providers/hashicorp/local/2.4.0/docs/resources/file) | resource |
| [local_file.ansible_confluent_platform_broker_public_key](https://registry.terraform.io/providers/hashicorp/local/2.4.0/docs/resources/file) | resource |
| [random_string.suffix](https://registry.terraform.io/providers/hashicorp/random/3.7.2/docs/resources/string) | resource |
| [tls_private_key.ansible_confluent_platform_broker_ssh_key](https://registry.terraform.io/providers/hashicorp/tls/4.0.6/docs/resources/private_key) | resource |
| [aws_availability_zones.available](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/data-sources/availability_zones) | data source |
| [aws_internet_gateway.existing_internet_gateway](https://registry.terraform.io/providers/hashicorp/aws/5.80.0/docs/data-sources/internet_gateway) | data source |
| [external_external.my_public_ip](https://registry.terraform.io/providers/hashicorp/external/2.3.4/docs/data-sources/external) | data source |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| <a name="input_ansible_control_node_subnet_cidr"></a> [ansible\_control\_node\_subnet\_cidr](#input\_ansible\_control\_node\_subnet\_cidr) | CIDR block for the public subnet | `string` | n/a | yes |
| <a name="input_aws_zones"></a> [aws\_zones](#input\_aws\_zones) | AWS Zones | <pre>list(object({<br/>    zone = string<br/>    cidr = string<br/>  }))</pre> | n/a | yes |
| <a name="input_vpc_id"></a> [vpc\_id](#input\_vpc\_id) | n/a | `string` | n/a | yes |

## Outputs

| Name | Description |
|------|-------------|
| <a name="output_aws_key_pair_name"></a> [aws\_key\_pair\_name](#output\_aws\_key\_pair\_name) | n/a |
| <a name="output_confluent_platform_broker_instances_security_group_id"></a> [confluent\_platform\_broker\_instances\_security\_group\_id](#output\_confluent\_platform\_broker\_instances\_security\_group\_id) | n/a |
| <a name="output_confluent_platform_broker_subnet_ids"></a> [confluent\_platform\_broker\_subnet\_ids](#output\_confluent\_platform\_broker\_subnet\_ids) | n/a |
| <a name="output_private_key"></a> [private\_key](#output\_private\_key) | n/a |
| <a name="output_private_link_security_group_id"></a> [private\_link\_security\_group\_id](#output\_private\_link\_security\_group\_id) | n/a |
| <a name="output_public_subnet_id"></a> [public\_subnet\_id](#output\_public\_subnet\_id) | n/a |
<!-- END_TF_DOCS -->
