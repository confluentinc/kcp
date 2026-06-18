package modules

import (
	"github.com/confluentinc/kcp/internal/services/hcl/hclrequests"
	"github.com/confluentinc/kcp/internal/services/hcl/hcltypes"
)

func GetJumpClusterSetupHostVariables() []ModuleVariable[hclrequests.MigrationWizardRequest] {
	return []ModuleVariable[hclrequests.MigrationWizardRequest]{
		{
			Name: "jump_cluster_setup_host_subnet_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "jump_cluster_setup_host_subnet_id",
				Description: "ID of the subnet that the jump cluster setup host (Ansible) instance is deployed to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name:       SchemaJumpClusterSecurityGroupIDs.Name,
			Definition: SchemaJumpClusterSecurityGroupIDs.ToDefinition(),
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return []string{}
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name:       SchemaJumpClusterSSHKeyPairName.Name,
			Definition: SchemaJumpClusterSSHKeyPairName.ToDefinition(),
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "jump_cluster_instances_private_dns",
			Definition: hcltypes.TerraformVariable{
				Name:        "jump_cluster_instances_private_dns",
				Description: "Private DNS addresses of the jump cluster instances.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return []string{}
			},
			Condition:        nil,
			FromModuleOutput: "jump_cluster",
		},
		{
			Name: "private_key",
			Definition: hcltypes.TerraformVariable{
				Name:        "private_key",
				Description: "Private SSH key for accessing the jump cluster (including setup host) instances.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
	}
}

func GetJumpClusterSetupHostVariableDefinitions(request hclrequests.MigrationWizardRequest) []hcltypes.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetJumpClusterSetupHostVariables(), request)
}
