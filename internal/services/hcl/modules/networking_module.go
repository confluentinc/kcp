package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetNetworkingVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name:       SchemaVpcID.Name,
			Definition: SchemaVpcID.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.VpcId
			},
			Condition: nil,
		},
		{
			Name: "jump_cluster_broker_subnet_cidrs",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_broker_subnet_cidrs",
				Description: "CIDR ranges of the jump cluster broker subnets",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.JumpClusterBrokerSubnetCidr
			},
			Condition: nil,
		},
		{
			Name: "jump_cluster_setup_host_subnet_cidr",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_setup_host_subnet_cidr",
				Description: "CIDR block of the jump cluster setup host subnet",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.JumpClusterSetupHostSubnetCidr
			},
			Condition: nil,
		},
		{
			Name: "existing_private_link_vpce_id",
			Definition: types.TerraformVariable{
				Name:        "existing_private_link_vpce_id",
				Description: "ID of the existing VPC endpoint for the Private Link connection to Confluent Cloud",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.ExistingPrivateLinkVpceId
			},
			Condition: nil,
		},
	}
}

func GetNetworkingModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetNetworkingVariables(), request)
}

var NetworkingModuleOutputs = []types.TerraformOutput{
	{
		Name:        "jump_cluster_setup_host_subnet_id",
		Description: "ID of the subnet that the Ansible jump cluster setup host instance is deployed to.",
		Sensitive:   false,
		Value:       "aws_subnet.jump_cluster_setup_host_subnet.id",
	},
	{
		Name:        "jump_cluster_broker_subnet_ids",
		Description: "IDs of the subnets that the jump cluster broker instances are deployed to.",
		Sensitive:   false,
		Value:       "aws_subnet.jump_cluster_broker_subnets[*].id",
	},
	{
		Name:        "jump_cluster_ssh_key_pair_name",
		Description: "Name of the AWS key pair for the jump cluster (including setup host) instances.",
		Sensitive:   false,
		Value:       "aws_key_pair.jump_cluster_ssh_key.key_name",
	},
	{
		Name:        "private_key",
		Description: "Private SSH key for accessing the jump cluster (including setup host) instances.",
		Sensitive:   true,
		Value:       "tls_private_key.jump_cluster_ssh_key.private_key_pem",
	},
	{
		Name:        "jump_cluster_security_group_ids",
		Description: "IDs of the security groups for the jump cluster (including setup host) instances.",
		Sensitive:   false,
		Value:       "aws_security_group.security_group.id",
	},
}

func GetNetworkingModuleOutputDefinitions() []types.TerraformOutput {
	return NetworkingModuleOutputs
}
