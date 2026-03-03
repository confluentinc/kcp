package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetTargetClusterPrivateLinkVariables() []ModuleVariable[types.TargetClusterWizardRequest] {
	return []ModuleVariable[types.TargetClusterWizardRequest]{
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
