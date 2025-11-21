package modules

import "github.com/confluentinc/kcp/internal/types"

func GetClusterLinkVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name: "msk_sasl_scram_username",
			Definition: types.TerraformVariable{
				Name:        "msk_sasl_scram_username",
				Description: "MSK SASL SCRAM Username",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return "" // User prompted for value at Terraform apply.
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "msk_sasl_scram_password",
			Definition: types.TerraformVariable{
				Name:        "msk_sasl_scram_password",
				Description: "MSK SASL SCRAM Password",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return "" // User prompted for value at Terraform apply.
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "confluent_cloud_cluster_api_key",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_api_key",
				Description: "Confluent Cloud cluster API key",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return "" // User prompted for value at Terraform apply.
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "confluent_cloud_cluster_api_secret",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_api_secret",
				Description: "Confluent Cloud cluster API secret",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return "" // User prompted for value at Terraform apply.
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "target_cluster_rest_endpoint",
			Definition: types.TerraformVariable{
				Name:        "target_cluster_rest_endpoint",
				Description: "The REST endpoint of the target Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.TargetRestEndpoint
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "target_cluster_id",
			Definition: types.TerraformVariable{
				Name:        "target_cluster_id",
				Description: "The ID of the target Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.TargetClusterId
			},
			Condition: nil,
			FromModuleOutput: "",
		},
		{
			Name: "cluster_link_name",
			Definition: types.TerraformVariable{
				Name:        "cluster_link_name",
				Description: "The name of the cluster link between the source and target Confluent Cloud clusters.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.ClusterLinkName
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "msk_cluster_id",
			Definition: types.TerraformVariable{
				Name:        "msk_cluster_id",
				Description: "The ID of the source MSK cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.MskClusterId
			},
			Condition:        nil,
			FromModuleOutput: "",
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
			Condition:        nil,
			FromModuleOutput: "",
		},
	}
}

func GetClusterLinkModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable
	clusterLinkVars := GetClusterLinkVariables()

	for _, varDef := range clusterLinkVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}
