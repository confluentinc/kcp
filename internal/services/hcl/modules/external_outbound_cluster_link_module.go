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
			Condition: nil,
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
			Condition: nil,
		},
		{
			Name: "confluent_cloud_cluster_api_key",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_api_key",
				Description: "API key of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition: nil,
		},
		{
			Name: "confluent_cloud_cluster_api_secret",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_api_secret",
				Description: "API secret of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition: nil,
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
			Condition: nil,
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
			Condition: nil,
		},
		{
			Name: "cluster_link_name",
			Definition: types.TerraformVariable{
				Name:        "cluster_link_name",
				Description: "Name of the cluster link that will be created between the source and target Confluent Cloud clusters.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.ClusterLinkName
			},
			Condition: nil,
		},
		{
			Name: "source_cluster_id",
			Definition: types.TerraformVariable{
				Name:        "source_cluster_id",
				Description: "ID of the source Kafka cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.SourceClusterId
			},
			Condition: nil,
		},
		{
			Name: "source_cluster_bootstrap_servers",
			Definition: types.TerraformVariable{
				Name:        "source_cluster_bootstrap_servers",
				Description: "Bootstrap brokers of the source Kafka cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				if request.MskJumpClusterAuthType == "unauth_tls" {
					return request.MskUnauthTlsBootstrapServers
				}
				return request.MskSaslScramBootstrapServers
			},
			Condition: nil,
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
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "source_sasl_scram_username",
			Definition: types.TerraformVariable{
				Name:        "source_sasl_scram_username",
				Description: "SASL SCRAM username of the source Kafka cluster that data will be migrated from.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition: func(request types.MigrationWizardRequest) bool {
				return request.MskJumpClusterAuthType != "unauth_tls"
			},
		},
		{
			Name: "source_sasl_scram_password",
			Definition: types.TerraformVariable{
				Name:        "source_sasl_scram_password",
				Description: "SASL SCRAM password of the source Kafka cluster that data will be migrated from.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return ""
			},
			Condition: func(request types.MigrationWizardRequest) bool {
				return request.MskJumpClusterAuthType != "unauth_tls"
			},
		},
	}
}

func GetExternalOutboundClusterLinkingModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetExternalOutboundClusterLinkingVariables(), request)
}
