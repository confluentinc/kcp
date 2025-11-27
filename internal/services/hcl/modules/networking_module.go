package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetNetworkingVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name: "vpc_id",
			Definition: types.TerraformVariable{
				Name:        "vpc_id",
				Description: "ID of the VPC",
				Sensitive:   false,
				Type:        "string",
			},
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
	}
}

func GetNetworkingModuleVariableNames() map[string]string {
	vars := GetNetworkingVariables()
	names := make(map[string]string)

	for _, v := range vars {
		names[v.Name] = v.Name
	}

	return names
}

func GetNetworkingModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable
	networkingVars := GetNetworkingVariables()

	for _, varDef := range networkingVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}

var NetworkingModuleOutputs = []ModuleOutputDefinition{
	{
		Name: "jump_cluster_setup_host_subnet_id",
		Definition: types.TerraformOutput{
			Name:        "jump_cluster_setup_host_subnet_id",
			Description: "ID of the subnet that the Ansible jump cluster setup host instance is deployed to.",
			Sensitive:   false,
			Value:       "aws_subnet.jump_cluster_setup_host_subnet.id",
		},
	},
	{
		Name: "jump_cluster_broker_subnet_ids",
		Definition: types.TerraformOutput{
			Name:        "jump_cluster_broker_subnet_ids",
			Description: "IDs of the subnets that the jump cluster broker instances are deployed to.",
			Sensitive:   false,
			Value:       "aws_subnet.jump_cluster_broker_subnets[*].id",
		},
	},
	{
		Name: "jump_cluster_ssh_key_pair_name",
		Definition: types.TerraformOutput{
			Name:        "jump_cluster_ssh_key_pair_name",
			Description: "Name of the AWS key pair for the jump cluster (including setup host) instances.",
			Sensitive:   false,
			Value:       "aws_key_pair.jump_cluster_ssh_key.key_name",
		},
	},
	{
		Name: "private_key",
		Definition: types.TerraformOutput{
			Name:        "private_key",
			Description: "Private SSH key for accessing the jump cluster (including setup host) instances.",
			Sensitive:   true,
			Value:       "tls_private_key.jump_cluster_ssh_key.private_key_pem",
		},
	},
	{
		Name: "jump_cluster_security_group_ids",
		Definition: types.TerraformOutput{
			Name:        "jump_cluster_security_group_ids",
			Description: "IDs of the security groups for the jump cluster (including setup host) instances.",
			Sensitive:   false,
			Value:       "aws_security_group.security_group.id",
		},
	},
	{
		Name: "private_link_security_group_id",
		Definition: types.TerraformOutput{
			Name:        "private_link_security_group_id",
			Description: "ID of the security group for the private link connection.",
			Sensitive:   false,
			Value:       "aws_security_group.private_link_security_group.id",
		},
	},
}

func GetNetworkingModuleOutputDefinitions() []types.TerraformOutput {
	var definitions []types.TerraformOutput

	for _, outputDef := range NetworkingModuleOutputs {
		definitions = append(definitions, outputDef.Definition)
	}

	return definitions
}
