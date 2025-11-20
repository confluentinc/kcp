package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

type VariableDefinition interface {
	GetName() string
	GetDefinition() types.TerraformVariable
}

type ModuleOutputDefinition struct {
	Name       string
	Definition types.TerraformOutput
}

// ModuleVariable is a generic definition for module variables.
// R is the request type (e.g. TargetClusterWizardRequest or MigrationWizardRequest).
type ModuleVariable[R any] struct {
	Name             string
	Definition       types.TerraformVariable
	ValueExtractor   func(request R) any  // Extracts the value from FE request payload. If nil, it's not a root-level variable.
	Condition        func(request R) bool // Determines if this variable should be included (nil = always include).
	FromModuleOutput string               // If non-empty, this variable comes from the named module's output.
}

func (m ModuleVariable[R]) GetName() string {
	return m.Name
}

func (m ModuleVariable[R]) GetDefinition() types.TerraformVariable {
	return m.Definition
}

// ============================================================================
// Target Cluster
// ============================================================================

func GetTargetClusterModuleVariableValues(request types.TargetClusterWizardRequest) map[string]any {
	allVars := []ModuleVariable[types.TargetClusterWizardRequest]{}
	allVars = append(allVars, GetTargetClusterProviderVariables()...) // aws_region
	allVars = append(allVars, GetConfluentCloudVariables()...)        // region
	allVars = append(allVars, GetTargetClusterPrivateLinkVariables()...)

	return extractVariableValues(allVars, request)
}

func GetTargetClusterModuleVariableDefinitions(request types.TargetClusterWizardRequest) []types.TerraformVariable {
	allVars := []ModuleVariable[types.TargetClusterWizardRequest]{}
	allVars = append(allVars, GetTargetClusterProviderVariables()...)
	allVars = append(allVars, GetConfluentCloudVariables()...)
	allVars = append(allVars, GetTargetClusterPrivateLinkVariables()...)

	return extractVariableDefinitions(allVars, request)
}

// ============================================================================
// Migration Infrastructure
// ============================================================================

func GetMigrationInfraRootVariableValues(request types.MigrationWizardRequest) map[string]any {
	// Collect variables from all modules
	allVars := []ModuleVariable[types.MigrationWizardRequest]{}
	if request.HasPublicCcEndpoints {
		allVars = append(allVars, GetPublicMigrationProviderVariables()...)
		allVars = append(allVars, GetClusterLinkVariables()...)
	} else {
		allVars = append(allVars, GetPrivateMigrationProviderVariables()...)
		allVars = append(allVars, GetNetworkingVariables()...)
		allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
		allVars = append(allVars, GetJumpClusterVariables()...)
		allVars = append(allVars, GetMigrationInfraPrivateLinkVariables()...)
	}

	return extractVariableValues(allVars, request)
}

// Collects all root-level variable definitions from all modules.
func GetMigrationInfraRootVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	// Collect variables from all modules
	allVars := []ModuleVariable[types.MigrationWizardRequest]{}
	if request.HasPublicCcEndpoints {
		allVars = append(allVars, GetPublicMigrationProviderVariables()...)
		allVars = append(allVars, GetClusterLinkVariables()...)
	} else {
		allVars = append(allVars, GetPrivateMigrationProviderVariables()...)
		allVars = append(allVars, GetNetworkingVariables()...)
		allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
		allVars = append(allVars, GetJumpClusterVariables()...)
		allVars = append(allVars, GetMigrationInfraPrivateLinkVariables()...)
	}

	return extractVariableDefinitions(allVars, request)
}

// ============================================================================
// Helpers
// ============================================================================

// extractVariableValues extracts root-level variable values from a collection of variable definitions.
// It filters by condition, skips variables without value extractors, and only includes non-empty values.
func extractVariableValues[R any](allVars []ModuleVariable[R], request R) map[string]any {
	values := make(map[string]any)

	for _, varDef := range allVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		// Variables with nil ValueExtractor are not root-level variables.
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

// extractVariableDefinitions extracts root-level variable definitions.
// It filters by condition and skips variables without value extractors or those coming from module outputs.
func extractVariableDefinitions[R any](allVars []ModuleVariable[R], request R) []types.TerraformVariable {
	var definitions []types.TerraformVariable

	for _, varDef := range allVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}

		// Variables with nil ValueExtractor are not root-level variables.
		if varDef.ValueExtractor == nil {
			continue
		}

		// Skip variables that come from module outputs
		if varDef.FromModuleOutput != "" {
			continue
		}

		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}

func toVariableDefinitions[R any](vars []ModuleVariable[R]) []VariableDefinition {
	result := make([]VariableDefinition, len(vars))
	for i, v := range vars {
		result[i] = v
	}
	return result
}

func GetModuleVariableName(moduleName string, varName string) string {
	var variables []VariableDefinition

	switch moduleName {
	case "provider_variables":
		variables = toVariableDefinitions(GetPrivateMigrationProviderVariables())
	case "jump_cluster_setup_host":
		variables = toVariableDefinitions(GetJumpClusterSetupHostVariables())
	case "jump_cluster":
		variables = toVariableDefinitions(GetJumpClusterVariables())
	case "networking":
		variables = toVariableDefinitions(GetNetworkingVariables())
	case "private_link_connection":
		variables = toVariableDefinitions(GetMigrationInfraPrivateLinkVariables())
	case "cluster_link":
		variables = toVariableDefinitions(GetClusterLinkVariables())
	case "confluent_cloud":
		variables = toVariableDefinitions(GetConfluentCloudVariables())
	case "private_link_target_cluster":
		variables = toVariableDefinitions(GetTargetClusterPrivateLinkVariables())
	default:
		return "<variable not found>"
	}

	for _, varDef := range variables {
		if varDef.GetName() == varName {
			return varDef.GetName()
		}
	}

	return "<variable not found>"
}
