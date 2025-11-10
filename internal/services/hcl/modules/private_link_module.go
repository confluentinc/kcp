package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetPrivateLinkModuleVariableNames() map[string]string {
	vars := GetPrivateLinkVariables()
	names := make(map[string]string)

	for _, v := range vars {
		names[v.Name] = v.Name
	}

	return names
}

func GetPrivateLinkModuleOutputDefinitions() []types.TerraformOutput {
	var definitions []types.TerraformOutput

	for _, outputDef := range PrivateLinkModuleOutputs {
		definitions = append(definitions, outputDef.Definition)
	}

	return definitions
}

func GetTargetClusterPrivateLinkVariables() []TargetClusterModulesVariableDefinition {
	return []TargetClusterModulesVariableDefinition{
		{
			Name: "aws_region",
			Definition: types.TerraformVariable{
				Name:        "aws_region",
				Description: "The AWS region of the VPC that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.AwsRegion
			},
			Condition: nil,
		},
		{
			Name: "vpc_id",
			Definition: types.TerraformVariable{
				Name:        "vpc_id",
				Description: "The ID of the VPC that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.VpcId
			},
			Condition: nil,
		},
		{
			Name: "subnet_cidr_ranges",
			Definition: types.TerraformVariable{
				Name:        "subnet_cidr_ranges",
				Description: "The CIDR ranges of the subnets that the private link connection is established in.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.SubnetCidrRanges
			},
			Condition: nil,
		},
		{
			Name: "environment_id",
			Definition: types.TerraformVariable{
				Name:        "environment_id",
				Description: "The ID of the environment that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: nil, // Retrieved from Confluent Cloud module output.
			Condition: nil,
		},
	}
}

func GetTargetClusterPrivateLinkModuleVariableDefinitions(request types.TargetClusterWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable
	privateLinkVars := GetTargetClusterPrivateLinkVariables()

	for _, varDef := range privateLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}

func GetPrivateLinkVariables() []MigrationInfraVariableDefinition {
	return []MigrationInfraVariableDefinition{
		{
			Name: "aws_region",
			Definition: types.TerraformVariable{
				Name:        "aws_region",
				Description: "The AWS region of the VPC that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.MskRegion
			},
			Condition: nil,
		},
		{
			Name: "vpc_id",
			Definition: types.TerraformVariable{
				Name:        "vpc_id",
				Description: "The ID of the VPC that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.VpcId
			},
			Condition: nil,
		},
		{
			Name: "jump_cluster_broker_subnet_ids",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_broker_subnet_ids",
				Description: "The IDs of the subnets that the jump cluster broker instances are deployed to.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: nil, // Retrieved from networking module output.
			Condition: nil,
		},
		{
			Name: "security_group_id",
			Definition: types.TerraformVariable{
				Name:        "security_group_id",
				Description: "The ID of the security group that the private link connection is established in.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: nil, // Retrieved from networking module output.
			Condition: nil,
		},
		{
			Name: "target_environment_id",
			Definition: types.TerraformVariable{
				Name:        "target_environment_id",
				Description: "The ID of the target environment.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.TargetEnvironmentId
			},
			Condition: nil,
		},
	}
}

func GetPrivateLinkModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable
	privateLinkVars := GetPrivateLinkVariables()

	for _, varDef := range privateLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}

var PrivateLinkModuleOutputs = []ModuleOutputDefinition{
	{
		Name: "private_link_attachment_id",
		Definition: types.TerraformOutput{
			Name:        "private_link_attachment_id",
			Description: "ID of the private link attachment.",
			Sensitive:   false,
			Value:       "confluent_private_link_attachment.aws.id",
		},
	},
	{
		Name: "vpc_endpoint_id",
		Definition: types.TerraformOutput{
			Name:        "vpc_endpoint_id",
			Description: "ID of the VPC endpoint.",
			Sensitive:   false,
			Value:       "values(aws_subnet.jump_cluster_broker_subnets)[*].id",
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
}