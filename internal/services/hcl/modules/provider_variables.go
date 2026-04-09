package modules

import (
	"github.com/confluentinc/kcp/internal/types"
)

func GetPublicMigrationProviderVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name:       SchemaConfluentCloudAPIKey.Name,
			Definition: SchemaConfluentCloudAPIKey.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaConfluentCloudAPISecret.Name,
			Definition: SchemaConfluentCloudAPISecret.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return ""
			},
		},
	}
}

func GetPrivateMigrationProviderVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name:       SchemaConfluentCloudAPIKey.Name,
			Definition: SchemaConfluentCloudAPIKey.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaConfluentCloudAPISecret.Name,
			Definition: SchemaConfluentCloudAPISecret.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaAWSRegion.Name,
			Definition: SchemaAWSRegion.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.SourceRegion
			},
			Condition: func(request types.MigrationWizardRequest) bool {
				return !request.HasPublicEndpoints
			},
		},
	}
}

func GetTargetClusterProviderVariables() []ModuleVariable[types.TargetClusterWizardRequest] {
	return []ModuleVariable[types.TargetClusterWizardRequest]{
		{
			Name:       SchemaConfluentCloudAPIKey.Name,
			Definition: SchemaConfluentCloudAPIKey.ToDefinition(),
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaConfluentCloudAPISecret.Name,
			Definition: SchemaConfluentCloudAPISecret.ToDefinition(),
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaAWSRegion.Name,
			Definition: SchemaAWSRegion.ToDefinition(),
			ValueExtractor: func(request types.TargetClusterWizardRequest) any {
				return request.AwsRegion
			},
		},
	}
}
