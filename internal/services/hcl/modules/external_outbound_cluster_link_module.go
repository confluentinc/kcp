package modules

import "github.com/confluentinc/kcp/internal/types"

func GetExternalOutboundClusterLinkingVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name: "subnet_id",
			Definition: types.TerraformVariable{
				Name:        "subnet_id",
				Description: "The subnet ID where the EC2 instance will be launched.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.ExtOutboundSubnetId
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "security_group_id",
			Definition: types.TerraformVariable{
				Name:        "security_group_id",
				Description: "The security group ID to attach to the EC2 instance.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.ExtOutboundSecurityGroupId
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "target_cluster_api_key",
			Definition: types.TerraformVariable{
				Name:        "target_cluster_api_key",
				Description: "API key of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "target_cluster_api_secret",
			Definition: types.TerraformVariable{
				Name:        "target_cluster_api_secret",
				Description: "API secret of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "target_cluster_rest_endpoint",
			Definition: types.TerraformVariable{
				Name:        "target_cluster_rest_endpoint",
				Description: "REST endpoint of the Confluent Cloud cluster that data will be migrated to.",
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
				Description: "ID of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.TargetClusterId
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "cluster_link_name",
			Definition: types.TerraformVariable{
				Name:        "cluster_link_name",
				Description: "Name of the cluster link between the source and target Confluent Cloud clusters.",
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
				Description: "ID of the source MSK cluster that data will be migrated from.",
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
			Name: "msk_cluster_bootstrap_servers",
			Definition: types.TerraformVariable{
				Name:        "msk_cluster_bootstrap_servers",
				Description: "Bootstrap brokers of the MSK cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.MskSaslScramBootstrapServers
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "msk_sasl_scram_username",
			Definition: types.TerraformVariable{
				Name:        "msk_sasl_scram_username",
				Description: "SASL SCRAM username of the source MSK cluster that data will be migrated from.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "msk_sasl_scram_password",
			Definition: types.TerraformVariable{
				Name:        "msk_sasl_scram_password",
				Description: "SASL SCRAM password of the source MSK cluster that data will be migrated from.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
	}
}

func GetExternalOutboundClusterLinkingModuleVariableNames() map[string]string {
	vars := GetExternalOutboundClusterLinkingVariables()
	names := make(map[string]string)

	for _, v := range vars {
		names[v.Name] = v.Name
	}

	return names
}

func GetExternalOutboundClusterLinkingModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable
	extOutboundClusterLinkingVars := GetExternalOutboundClusterLinkingVariables()

	for _, varDef := range extOutboundClusterLinkingVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}
