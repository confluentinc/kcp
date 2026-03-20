package modules

import "github.com/confluentinc/kcp/internal/types"

func GetClusterLinkVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name:       SchemaMSKSaslScramUsername.Name,
			Definition: SchemaMSKSaslScramUsername.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaMSKSaslScramPassword.Name,
			Definition: SchemaMSKSaslScramPassword.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaConfluentCloudClusterAPIKey.Name,
			Definition: SchemaConfluentCloudClusterAPIKey.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaConfluentCloudClusterAPISecret.Name,
			Definition: SchemaConfluentCloudClusterAPISecret.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaTargetClusterRestEndpoint.Name,
			Definition: SchemaTargetClusterRestEndpoint.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.TargetRestEndpoint
			},
		},
		{
			Name:       SchemaTargetClusterID.Name,
			Definition: SchemaTargetClusterID.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.TargetClusterId
			},
		},
		{
			Name:       SchemaClusterLinkName.Name,
			Definition: SchemaClusterLinkName.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.ClusterLinkName
			},
		},
		{
			Name:       SchemaMSKClusterID.Name,
			Definition: SchemaMSKClusterID.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.MskClusterId
			},
		},
		{
			Name: "msk_sasl_scram_bootstrap_servers",
			Definition: types.TerraformVariable{
				Name:        "msk_sasl_scram_bootstrap_servers",
				Description: "The SASL/SCRAM bootstrap servers of the source MSK cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.MskSaslScramBootstrapServers
			},
		},
	}
}

func GetClusterLinkModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetClusterLinkVariables(), request)
}
