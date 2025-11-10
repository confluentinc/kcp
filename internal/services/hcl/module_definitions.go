package hcl

import (
	"github.com/confluentinc/kcp/internal/types"
)

type ModuleVariableDefinition struct {
	Name           string
	Definition     types.TerraformVariable
	ValueExtractor func(request types.MigrationWizardRequest) any  // Extracts the value from FE request payload.
	Condition      func(request types.MigrationWizardRequest) bool // Determines if this variable should be included (nil = always include).
}

type ModuleOutputDefinition struct {
	Name       string
	Definition types.TerraformOutput
}

func getAllModuleVariables() []ModuleVariableDefinition {
	var allVars []ModuleVariableDefinition
	allVars = append(allVars, GetProviderVariables()...)
	allVars = append(allVars, GetNetworkingVariables()...)
	allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
	return allVars
}

var MigrationInfraModuleVariables = getAllModuleVariables()

func GetModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable

	for _, varDef := range MigrationInfraModuleVariables {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}

func GetModuleVariableValues(request types.MigrationWizardRequest) map[string]any {
	values := make(map[string]any)

	for _, varDef := range MigrationInfraModuleVariables {
		// Check condition if present
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		values[varDef.Name] = varDef.ValueExtractor(request)
	}

	return values
}

// GetRootLevelVariableValues collects all root-level variables (those passed from root to modules,
// not from module outputs) from all modules. This is used to populate inputs.auto.tfvars.
func GetRootLevelVariableValues(request types.MigrationWizardRequest) map[string]any {
	values := make(map[string]any)

	// Collect variables from all modules
	allVars := []ModuleVariableDefinition{}
	allVars = append(allVars, GetProviderVariables()...)
	allVars = append(allVars, GetNetworkingVariables()...)
	allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
	allVars = append(allVars, GetJumpClusterVariables()...)
	allVars = append(allVars, GetPrivateLinkVariables()...)

	for _, varDef := range allVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		// Variables with non-nil ValueExtractor are root-level variables.
		if varDef.ValueExtractor == nil {
			continue
		}

		value := varDef.ValueExtractor(request)

		// Only include non-empty values
		switch v := value.(type) {
		case string:
			if v != "" {
				values[varDef.Name] = v
			}
		case []string:
			if len(v) > 0 {
				values[varDef.Name] = v
			}
		case bool:
			values[varDef.Name] = v
		case int:
			values[varDef.Name] = v
		}
	}

	return values
}

// GetRootLevelVariableDefinitions collects all root-level variable definitions (those passed from root to modules,
// not from module outputs) from all modules. This is used to populate the root variables.tf file.
// Note: This includes variables even if they have empty values (like API keys that are user-provided at Terraform apply time).
func GetRootLevelVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable

	allVars := []ModuleVariableDefinition{}
	allVars = append(allVars, GetProviderVariables()...)
	allVars = append(allVars, GetNetworkingVariables()...)
	allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
	allVars = append(allVars, GetJumpClusterVariables()...)
	allVars = append(allVars, GetPrivateLinkVariables()...)

	for _, varDef := range allVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		// Variables with non-nil ValueExtractor are root-level variables.
		if varDef.ValueExtractor == nil {
			continue
		}

		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}

// GetModuleVariableName searches for a variable name within a specific module's variable definitions.
// moduleName should be one of: "jump_cluster_setup_host", "jump_clusters", "networking", "private_link_connection"
func GetModuleVariableName(moduleName string, varName string) string {
	var variables []ModuleVariableDefinition

	switch moduleName {
	case "jump_cluster_setup_host":
		variables = GetJumpClusterSetupHostVariables()
	case "jump_clusters":
		variables = GetJumpClusterVariables()
	case "networking":
		variables = GetNetworkingVariables()
	case "private_link_connection":
		variables = GetPrivateLinkVariables()
	default:
		return "<variable not found>"
	}

	for _, varDef := range variables {
		if varDef.Name == varName {
			return varDef.Name
		}
	}

	return "<variable not found>"
}
