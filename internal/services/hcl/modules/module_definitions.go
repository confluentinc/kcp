package modules

import (
	"log/slog"

	"github.com/confluentinc/kcp/internal/types"
)

// ModuleVariable is a generic definition for module variables.
// R is the request type (e.g. TargetClusterWizardRequest or MigrationWizardRequest).
type ModuleVariable[R any] struct {
	// Name is the module input attribute name (the key in the module block).
	// This may differ from Definition.Name (the root-level variable name).
	// WriteModuleInputs uses Name for the attribute key and Definition.Name for the var. reference.
	Name             string
	Definition       types.TerraformVariable
	ValueExtractor   func(request R) any  // Extracts the value from FE request payload. If nil, it's not a root-level variable.
	Condition        func(request R) bool // Determines if this variable should be included (nil = always include).
	FromModuleOutput string               // If non-empty, this variable comes from the named module's output.
}

// NOTE: VariableSchema migration is incomplete. Currently only cluster_link_module.go and
// provider_variables.go use VariableSchema to define variable metadata; remaining modules
// still define types.TerraformVariable inline and are candidates for migration.

// ============================================================================
// Target Cluster
// ============================================================================

func collectTargetClusterVars() []ModuleVariable[types.TargetClusterWizardRequest] {
	var allVars []ModuleVariable[types.TargetClusterWizardRequest]
	allVars = append(allVars, GetTargetClusterProviderVariables()...)
	allVars = append(allVars, GetConfluentCloudVariables()...)
	allVars = append(allVars, GetTargetClusterPrivateLinkVariables()...)
	return allVars
}

func GetTargetClusterModuleVariableValues(request types.TargetClusterWizardRequest) map[string]any {
	return extractVariableValues(collectTargetClusterVars(), request)
}

func GetTargetClusterModuleVariableDefinitions(request types.TargetClusterWizardRequest) []types.TerraformVariable {
	return extractVariableDefinitions(collectTargetClusterVars(), request)
}

// ============================================================================
// Migration Infrastructure
// ============================================================================

func collectMigrationInfraVars(request types.MigrationWizardRequest) []ModuleVariable[types.MigrationWizardRequest] {
	var allVars []ModuleVariable[types.MigrationWizardRequest]
	if request.HasPublicMskEndpoints {
		allVars = append(allVars, GetPublicMigrationProviderVariables()...)
		allVars = append(allVars, GetClusterLinkVariables()...)
	} else if request.UseJumpClusters {
		allVars = append(allVars, GetPrivateMigrationProviderVariables()...)
		allVars = append(allVars, GetNetworkingVariables()...)
		allVars = append(allVars, GetJumpClusterSetupHostVariables()...)
		allVars = append(allVars, GetJumpClusterVariables()...)
	} else {
		allVars = append(allVars, GetPrivateMigrationProviderVariables()...)
		allVars = append(allVars, GetMskPrivateClusterLinkVariables()...)
		allVars = append(allVars, GetExternalOutboundClusterLinkingVariables()...)
	}
	return allVars
}

func GetMigrationInfraRootVariableValues(request types.MigrationWizardRequest) map[string]any {
	return extractVariableValues(collectMigrationInfraVars(request), request)
}

func GetMigrationInfraRootVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	return extractVariableDefinitions(collectMigrationInfraVars(request), request)
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

		if _, exists := values[varDef.Definition.Name]; exists {
			slog.Warn("conflicting variable values, keeping first occurrence",
				"variable", varDef.Definition.Name,
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

