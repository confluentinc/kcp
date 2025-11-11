package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetJumpClusterSetupHostVariables() []MigrationInfraVariableDefinition {
	return []MigrationInfraVariableDefinition{
		{
			Name: "jump_cluster_setup_host_subnet_id",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_setup_host_subnet_id",
				Description: "ID of the subnet that the jump cluster setup host (Ansible) instance is deployed to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "jump_cluster_security_group_ids",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_security_group_ids",
				Description: "IDs of the security groups for the jump cluster (including setup host) instances.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return []string{}
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "jump_cluster_ssh_key_pair_name",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_ssh_key_pair_name",
				Description: "Name of the AWS key pair for SSH access to the jump cluster (including setup host) instances.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "jump_cluster_broker_subnet_ids",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_broker_subnet_ids",
				Description: "IDs of the subnets that the jump cluster broker instances are deployed to.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return []string{}
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "private_key",
			Definition: types.TerraformVariable{
				Name:        "private_key",
				Description: "Private SSH key for accessing the jump cluster (including setup host) instances.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
	}
}

func GetJumpClusterSetupHostVariableNames() map[string]string {
	vars := GetJumpClusterSetupHostVariables()
	names := make(map[string]string)

	for _, v := range vars {
		names[v.Name] = v.Name
	}

	return names
}

func GetJumpClusterSetupHostVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable
	jumpClusterSetupHostVars := GetJumpClusterSetupHostVariables()

	for _, varDef := range jumpClusterSetupHostVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}
