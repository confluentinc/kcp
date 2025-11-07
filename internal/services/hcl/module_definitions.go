package hcl

import (
	"github.com/confluentinc/kcp/internal/types"
)

type ModuleVariableDefinition struct {
	Name string
	Definition types.TerraformVariable
	ValueExtractor func(request types.MigrationWizardRequest) any // Extracts the value from FE request payload.
	Condition func(request types.MigrationWizardRequest) bool // Determines if this variable should be included (nil = always include).
}

type ModuleOutputDefinition struct {
	Name string
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

func GetModuleVariableName(varName string) string {
	for _, varDef := range MigrationInfraModuleVariables {
		if varDef.Name == varName {
			return varDef.Name
		}
	}
	return varName
}
