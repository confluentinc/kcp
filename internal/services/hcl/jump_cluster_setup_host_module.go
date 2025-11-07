package hcl

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetJumpClusterSetupHostVariables() []ModuleVariableDefinition {
	return []ModuleVariableDefinition{
		{
			Name: "jump_cluster_setup_host_subnet_id",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_setup_host_subnet_id",
				Description: "ID of the subnet that the jump cluster setup host (Ansible) instance is deployed to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return "" // Retrieved from networking module output.
			},
			Condition: nil,
		},
		{
			Name: "security_group_ids",
			Definition: types.TerraformVariable{
				Name:        "security_group_ids",
				Description: "IDs of the security groups for the jump cluster (including setup host) instances.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return []string{} // Retrieved from networking module output.
			},
			Condition: nil,
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
				return "" // Retrieved from networking module output.
			},
			Condition: nil,
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
				return []string{} // Retrieved from networking module output.
			},
			Condition: nil,
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
				return "" // Retrieved from networking module output.
			},
			Condition: nil,
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

func GetJumpClusterSetupHostVariableDefinitions() []types.TerraformVariable {
	var definitions []types.TerraformVariable
	jumpClusterVars := GetJumpClusterSetupHostVariables()

	for _, varDef := range jumpClusterVars {
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}
