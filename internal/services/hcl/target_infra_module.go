package hcl

import "github.com/confluentinc/kcp/internal/types"

func GetTargetClusterVariables() []TargetClusterModulesVariableDefinition {
	return []TargetClusterModulesVariableDefinition{
		{
			Name: "aws_region",
			Definition: types.TerraformVariable{
				Name:        "aws_region",
				Description: "Region of the cluster",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.Region
			},
			Condition: nil,
		},
		{
			Name: "environment_name",
			Definition: types.TerraformVariable{
				Name:        "environment_name",
				Description: "Name of the environment",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.EnvironmentName
			},
			Condition: func(request types.TargetClusterWizardRequest) bool {
				return request.NeedsEnvironment
			},
		},
		{
			Name: "environment_id",
			Definition: types.TerraformVariable{
				Name:        "environment_id",
				Description: "ID of the environment",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.EnvironmentId
			},
			Condition: func(request types.TargetClusterWizardRequest) bool {
				return !request.NeedsEnvironment
			},
		},
		{
			Name: "cluster_name",
			Definition: types.TerraformVariable{
				Name:        "cluster_name",
				Description: "Name of the cluster",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.ClusterName
			},
			Condition: func(request types.TargetClusterWizardRequest) bool {
				if request.NeedsEnvironment || request.NeedsCluster {
					return true
				} else {
					return false
				}
			},
		},
		{
			Name: "cluster_type",
			Definition: types.TerraformVariable{
				Name:        "cluster_type",
				Description: "Type of the cluster",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.ClusterType
			},
			Condition: func(request types.TargetClusterWizardRequest) bool {
				if request.NeedsEnvironment || request.NeedsCluster {
					return true
				} else {
					return false
				}
			},
		},
		{
			Name: "vpc_id",
			Definition: types.TerraformVariable{
				Name:        "vpc_id",
				Description: "ID of the VPC",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.VpcId
			},
			Condition: func(request types.TargetClusterWizardRequest) bool {
				return request.NeedsPrivateLink
			},
		},
		{
			Name: "subnet_cidr_ranges",
			Definition: types.TerraformVariable{
				Name:        "subnet_cidr_ranges",
				Description: "CIDR ranges of the subnets",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.SubnetCidrRanges
			},
			Condition: func(request types.TargetClusterWizardRequest) bool {
				return request.NeedsPrivateLink
			},
		},
	}
}

func GetTargetClusterModuleVariableDefinitions(request types.TargetClusterWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable

	// Collect variables from all sources
	allVars := []TargetClusterModulesVariableDefinition{}
	allVars = append(allVars, GetTargetClusterProviderVariables()...)
	allVars = append(allVars, GetTargetClusterVariables()...)

	// Use a map to track which variables we've already added (to avoid duplicates)
	seen := make(map[string]bool)

	for _, varDef := range allVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		// Skip if we've already added this variable
		if seen[varDef.Name] {
			continue
		}

		definitions = append(definitions, varDef.Definition)
		seen[varDef.Name] = true
	}
	return definitions
}

var TargetClusterModuleOutputs = []ModuleOutputDefinition{
	{
		Name: "cluster_id",
		Definition: types.TerraformOutput{
			Name:        "cluster_id",
			Description: "ID of the cluster",
			Sensitive:   false,
			Value:       "aws_msk_cluster.cluster.id",
		},
	},
}

func GetTargetClusterModuleOutputDefinitions() []types.TerraformOutput {
	var definitions []types.TerraformOutput

	for _, outputDef := range TargetClusterModuleOutputs {
		definitions = append(definitions, outputDef.Definition)
	}

	return definitions
}
