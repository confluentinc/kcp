package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetJumpClusterSetupHostVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
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
			Name:       SchemaJumpClusterSecurityGroupIDs.Name,
			Definition: SchemaJumpClusterSecurityGroupIDs.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return []string{}
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name:       SchemaJumpClusterSSHKeyPairName.Name,
			Definition: SchemaJumpClusterSSHKeyPairName.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "jump_cluster_instances_private_dns",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_instances_private_dns",
				Description: "Private DNS addresses of the jump cluster instances.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return []string{}
			},
			Condition:        nil,
			FromModuleOutput: "jump_cluster",
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

func GetJumpClusterSetupHostVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetJumpClusterSetupHostVariables(), request)
}
