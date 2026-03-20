package modules

import (
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
)

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
	if request.HasPublicMskEndpoints {
		allVars = append(allVars, GetPublicMigrationProviderVariables()...)
		allVars = append(allVars, GetClusterLinkVariables()...)
	} else if request.UseJumpClusters {
		allVars = append(allVars, GetPrivateMigrationProviderVariables()...)
		allVars = append(allVars, GetNetworkingVariables()...)
		allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
		allVars = append(allVars, GetJumpClusterVariables()...)
	} else {
		// External outbound cluster linking
		allVars = append(allVars, GetPrivateMigrationProviderVariables()...)
		allVars = append(allVars, GetMskPrivateClusterLinkVariables()...)
		allVars = append(allVars, GetExternalOutboundClusterLinkingVariables()...)
	}

	return extractVariableValues(allVars, request)
}

// Collects all root-level variable definitions from all modules.
func GetMigrationInfraRootVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	// Collect variables from all modules
	allVars := []ModuleVariable[types.MigrationWizardRequest]{}
	if request.HasPublicMskEndpoints {
		allVars = append(allVars, GetPublicMigrationProviderVariables()...)
		allVars = append(allVars, GetClusterLinkVariables()...)
	} else if request.UseJumpClusters {
		allVars = append(allVars, GetPrivateMigrationProviderVariables()...)
		allVars = append(allVars, GetNetworkingVariables()...)
		allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
		allVars = append(allVars, GetJumpClusterVariables()...)
	} else {
		// External outbound cluster linking
		allVars = append(allVars, GetPrivateMigrationProviderVariables()...)
		allVars = append(allVars, GetMskPrivateClusterLinkVariables()...)
		allVars = append(allVars, GetExternalOutboundClusterLinkingVariables()...)
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

		// Only include non-empty values, keyed by Definition.Name for deduplication
		switch v := value.(type) {
		case string:
			if v == "" {
				continue
			}
		case []string:
			if len(v) == 0 {
				continue
			}
		case []types.ExtOutboundClusterKafkaBroker:
			if len(v) == 0 {
				continue
			}
		}

		if existing, exists := values[varDef.Definition.Name]; exists {
			slog.Warn("conflicting variable values, keeping first occurrence",
				"variable", varDef.Definition.Name,
				"existing", existing,
				"ignored", value,
			)
			continue
		}
		values[varDef.Definition.Name] = value
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

