package modules

import "github.com/confluentinc/kcp/internal/services/hcl/hclrequests"

func GetPublicMigrationProviderVariables() []ModuleVariable[hclrequests.MigrationWizardRequest] {
	return []ModuleVariable[hclrequests.MigrationWizardRequest]{
		{
			Name:       SchemaConfluentCloudAPIKey.Name,
			Definition: SchemaConfluentCloudAPIKey.ToDefinition(),
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaConfluentCloudAPISecret.Name,
			Definition: SchemaConfluentCloudAPISecret.ToDefinition(),
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return ""
			},
		},
	}
}

func GetPrivateMigrationProviderVariables() []ModuleVariable[hclrequests.MigrationWizardRequest] {
	return []ModuleVariable[hclrequests.MigrationWizardRequest]{
		{
			Name:       SchemaConfluentCloudAPIKey.Name,
			Definition: SchemaConfluentCloudAPIKey.ToDefinition(),
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaConfluentCloudAPISecret.Name,
			Definition: SchemaConfluentCloudAPISecret.ToDefinition(),
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaAWSRegion.Name,
			Definition: SchemaAWSRegion.ToDefinition(),
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.SourceRegion
			},
			Condition: func(request hclrequests.MigrationWizardRequest) bool {
				return !request.HasPublicEndpoints
			},
		},
	}
}

func GetTargetClusterProviderVariables() []ModuleVariable[hclrequests.TargetClusterWizardRequest] {
	return []ModuleVariable[hclrequests.TargetClusterWizardRequest]{
		{
			Name:       SchemaConfluentCloudAPIKey.Name,
			Definition: SchemaConfluentCloudAPIKey.ToDefinition(),
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaConfluentCloudAPISecret.Name,
			Definition: SchemaConfluentCloudAPISecret.ToDefinition(),
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaAWSRegion.Name,
			Definition: SchemaAWSRegion.ToDefinition(),
			ValueExtractor: func(request hclrequests.TargetClusterWizardRequest) any {
				return request.AwsRegion
			},
		},
	}
}
