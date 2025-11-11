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

// ============================================================================
// Target Cluster
// ============================================================================

type TargetClusterModulesVariableDefinition struct {
	Name           string
	Definition     types.TerraformVariable
	ValueExtractor func(request types.TargetClusterWizardRequest) any  // Extracts the value from FE request payload.
	Condition      func(request types.TargetClusterWizardRequest) bool // Determines if this variable should be included (nil = always include).
}

func (t TargetClusterModulesVariableDefinition) GetName() string {
	return t.Name
}

func (t TargetClusterModulesVariableDefinition) GetDefinition() types.TerraformVariable {
	return t.Definition
}

func GetTargetClusterModuleVariableValues(request types.TargetClusterWizardRequest) map[string]any {
	allVars := []TargetClusterModulesVariableDefinition{}
	allVars = append(allVars, GetTargetClusterProviderVariables()...) // aws_region
	allVars = append(allVars, GetConfluentCloudVariables()...)        // region
	allVars = append(allVars, GetTargetClusterPrivateLinkVariables()...)

	return extractRootLevelVariableValues(
		allVars,
		request,
		func(v TargetClusterModulesVariableDefinition) string { return v.Name },
		func(v TargetClusterModulesVariableDefinition) func(types.TargetClusterWizardRequest) bool {
			return v.Condition
		},
		func(v TargetClusterModulesVariableDefinition) func(types.TargetClusterWizardRequest) any {
			return v.ValueExtractor
		},
	)
}

func GetTargetClusterModuleVariableDefinitions(request types.TargetClusterWizardRequest) []types.TerraformVariable {
	allVars := []TargetClusterModulesVariableDefinition{}
	allVars = append(allVars, GetTargetClusterProviderVariables()...)
	allVars = append(allVars, GetConfluentCloudVariables()...)
	allVars = append(allVars, GetTargetClusterPrivateLinkVariables()...)

	return extractRootLevelVariableDefinitions(
		allVars,
		request,
		func(v TargetClusterModulesVariableDefinition) types.TerraformVariable { return v.Definition },
		func(v TargetClusterModulesVariableDefinition) func(types.TargetClusterWizardRequest) bool {
			return v.Condition
		},
		func(v TargetClusterModulesVariableDefinition) func(types.TargetClusterWizardRequest) any {
			return v.ValueExtractor
		},
		func(v TargetClusterModulesVariableDefinition) string { return "" }, // TargetClusterModulesVariableDefinition doesn't have FromModuleOutput field
	)
}

// ============================================================================
// Migration Infrastructure
// ============================================================================

type MigrationInfraVariableDefinition struct {
	Name             string
	Definition       types.TerraformVariable
	ValueExtractor   func(request types.MigrationWizardRequest) any  // Extracts the value from FE request payload.
	Condition        func(request types.MigrationWizardRequest) bool // Determines if this variable should be included (nil = always include).
	FromModuleOutput string // If non-empty, this variable comes from the named module's output.
}

func (m MigrationInfraVariableDefinition) GetName() string {
	return m.Name
}

func (m MigrationInfraVariableDefinition) GetDefinition() types.TerraformVariable {
	return m.Definition
}

func GetMigrationInfraRootVariableValues(request types.MigrationWizardRequest) map[string]any {
	// Collect variables from all modules
	allVars := []MigrationInfraVariableDefinition{}
	allVars = append(allVars, GetProviderVariables()...)
	allVars = append(allVars, GetNetworkingVariables()...)
	allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
	allVars = append(allVars, GetJumpClusterVariables()...)
	allVars = append(allVars, GetMigrationInfraPrivateLinkVariables()...)

	return extractRootLevelVariableValues(
		allVars,
		request,
		func(v MigrationInfraVariableDefinition) string { return v.Name },
		func(v MigrationInfraVariableDefinition) func(types.MigrationWizardRequest) bool { return v.Condition },
		func(v MigrationInfraVariableDefinition) func(types.MigrationWizardRequest) any {
			return v.ValueExtractor
		},
	)
}

// Collects all root-level variable definitions from all modules.
func GetMigrationInfraRootVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	// Collect variables from all modules
	allVars := []MigrationInfraVariableDefinition{}
	allVars = append(allVars, GetProviderVariables()...)
	allVars = append(allVars, GetNetworkingVariables()...)
	allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
	allVars = append(allVars, GetJumpClusterVariables()...)
	allVars = append(allVars, GetMigrationInfraPrivateLinkVariables()...)

	return extractRootLevelVariableDefinitions(
		allVars,
		request,
		func(v MigrationInfraVariableDefinition) types.TerraformVariable { return v.Definition },
		func(v MigrationInfraVariableDefinition) func(types.MigrationWizardRequest) bool { return v.Condition },
		func(v MigrationInfraVariableDefinition) func(types.MigrationWizardRequest) any {
			return v.ValueExtractor
		},
		func(v MigrationInfraVariableDefinition) string { return v.FromModuleOutput },
	)
}

func toVariableDefinitions(vars []MigrationInfraVariableDefinition) []VariableDefinition {
	result := make([]VariableDefinition, len(vars))
	for i, v := range vars {
		result[i] = v
	}
	return result
}

// ============================================================================
// Helpers
// ============================================================================

// extractRootLevelVariableValues is a generic helper function that extracts root-level variable values
// from a collection of variable definitions. It filters by condition, skips variables without value extractors,
// and only includes non-empty values.
// V is the variable definition type, R is the request type.
func extractRootLevelVariableValues[V any, R any](
	allVars []V,
	request R,
	getName func(V) string,
	getCondition func(V) func(R) bool,
	getValueExtractor func(V) func(R) any,
) map[string]any {
	values := make(map[string]any)

	for _, varDef := range allVars {
		condition := getCondition(varDef)
		if condition != nil && !condition(request) {
			continue
		}

		valueExtractor := getValueExtractor(varDef)
		// Variables with non-nil ValueExtractor are root-level variables.
		if valueExtractor == nil {
			continue
		}

		value := valueExtractor(request)

		// Only include non-empty values
		switch v := value.(type) {
		case string:
			if v != "" {
				values[getName(varDef)] = v
			}
		case []string:
			if len(v) > 0 {
				values[getName(varDef)] = v
			}
		case bool:
			values[getName(varDef)] = v
		case int:
			values[getName(varDef)] = v
		}
	}

	return values
}

// extractRootLevelVariableDefinitions is a generic helper function that extracts root-level variable definitions
// from a collection of variable definitions. It filters by condition and skips variables without value extractors.
// Note: This includes variables even if they have empty values (like API keys that are user-provided at Terraform apply time).
// Variables with non-empty FromModuleOutput are excluded as they come from module outputs, not user input.
// V is the variable definition type, R is the request type.
func extractRootLevelVariableDefinitions[V any, R any](
	allVars []V,
	request R,
	getDefinition func(V) types.TerraformVariable,
	getCondition func(V) func(R) bool,
	getValueExtractor func(V) func(R) any,
	getFromModuleOutput func(V) string, // Function to get the module name if variable comes from module output (empty string = not from module output)
) []types.TerraformVariable {
	var definitions []types.TerraformVariable

	for _, varDef := range allVars {
		condition := getCondition(varDef)
		if condition != nil && !condition(request) {
			continue
		}

		valueExtractor := getValueExtractor(varDef)
		// Variables with non-nil ValueExtractor are root-level variables.
		if valueExtractor == nil {
			continue
		}

		// Skip variables that come from module outputs (non-empty module name)
		if getFromModuleOutput != nil && getFromModuleOutput(varDef) != "" {
			continue
		}

		definitions = append(definitions, getDefinition(varDef))
	}

	return definitions
}

func toTargetVariableDefinitions(vars []TargetClusterModulesVariableDefinition) []VariableDefinition {
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
		variables = toVariableDefinitions(GetProviderVariables())
	case "jump_cluster_setup_host":
		variables = toVariableDefinitions(GetJumpClusterSetupHostVariables())
	case "jump_clusters":
		variables = toVariableDefinitions(GetJumpClusterVariables())
	case "networking":
		variables = toVariableDefinitions(GetNetworkingVariables())
	case "private_link_connection":
		variables = toVariableDefinitions(GetMigrationInfraPrivateLinkVariables())
	case "confluent_cloud":
		variables = toTargetVariableDefinitions(GetConfluentCloudVariables())
	case "private_link_target_cluster":
		variables = toTargetVariableDefinitions(GetTargetClusterPrivateLinkVariables())
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
