package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

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
			ValueExtractor: nil,
			Condition:      nil,
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

func GetMigrationInfraPrivateLinkModuleVariableNames() map[string]string {
	vars := GetMigrationInfraPrivateLinkVariables()
	names := make(map[string]string)

	for _, v := range vars {
		names[v.Name] = v.Name
	}

	return names
}

func GetMigrationInfraPrivateLinkVariables() []MigrationInfraVariableDefinition {
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
			Condition:        nil,
			FromModuleOutput: "",
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
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "jump_cluster_broker_subnet_ids",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_broker_subnet_ids",
				Description: "The IDs of the subnets that the jump cluster broker instances are deployed to.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor:   nil,
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "security_group_id",
			Definition: types.TerraformVariable{
				Name:        "security_group_id",
				Description: "The ID of the security group that the private link connection is established in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor:   nil,
			Condition:        nil,
			FromModuleOutput: "networking",
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
			Condition:        nil,
			FromModuleOutput: "",
		},
	}
}

func GetMigrationInfraPrivateLinkModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable
	privateLinkVars := GetMigrationInfraPrivateLinkVariables()

	for _, varDef := range privateLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}
