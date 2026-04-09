package modules

import "github.com/confluentinc/kcp/internal/types"

func GetClusterLinkVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name:       SchemaSaslScramUsername.Name,
			Definition: SchemaSaslScramUsername.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
		},
		{
			Name:       SchemaSaslScramPassword.Name,
			Definition: SchemaSaslScramPassword.ToDefinition(),
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
			Name:       SchemaClusterID.Name,
			Definition: SchemaClusterID.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.SourceClusterId
			},
		},
		{
			Name:       SchemaSaslScramBootstrapServers.Name,
			Definition: SchemaSaslScramBootstrapServers.ToDefinition(),
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.SourceSaslScramBootstrapServers
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "source_sasl_scram_mechanism",
			Definition: types.TerraformVariable{
				Name:        "source_sasl_scram_mechanism",
				Description: "The SASL/SCRAM mechanism of the source Kafka cluster (SCRAM-SHA-256 or SCRAM-SHA-512).",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.SourceSaslScramMechanism
			},
		},
	}
}

func GetClusterLinkModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetClusterLinkVariables(), request)
}
