package modules

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

func GetConfluentCloudVariables() []TargetClusterModulesVariableDefinition {
	return []TargetClusterModulesVariableDefinition{
		{
			Name: "region",
			Definition: types.TerraformVariable{
				Name:        "region",
				Description: "The AWS region in which the Confluent Cloud cluster is provisioned in.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.AwsRegion
			},
			Condition: nil,
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
	}
}

func GetConfluentCloudVariableDefinitions(request types.TargetClusterWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable
	confluentCloudVars := GetConfluentCloudVariables()

	for _, varDef := range confluentCloudVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}

func GetConfluentCloudModuleOutputDefinitions(request types.TargetClusterWizardRequest, envResourceName string) []types.TerraformOutput {
	var definitions []types.TerraformOutput

	var envIdValue string
	if request.NeedsEnvironment {
		envIdValue = fmt.Sprintf("confluent_environment.%s.id", envResourceName)
	} else {
		envIdValue = fmt.Sprintf("data.confluent_environment.%s.id", envResourceName)
	}

	definitions = append(definitions, types.TerraformOutput{
		Name:        "environment_id",
		Description: "ID of the environment",
		Sensitive:   false,
		Value:       envIdValue,
	})

	return definitions
}