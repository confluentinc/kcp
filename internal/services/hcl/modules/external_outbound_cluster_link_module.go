package modules

import (
	"github.com/confluentinc/kcp/internal/services/hcl/hclrequests"
	"github.com/confluentinc/kcp/internal/services/hcl/hcltypes"
)

func GetExternalOutboundClusterLinkingVariables() []ModuleVariable[hclrequests.MigrationWizardRequest] {
	return []ModuleVariable[hclrequests.MigrationWizardRequest]{
		{
			Name: "subnet_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "subnet_id",
				Description: "The subnet ID where the EC2 instance will be launched.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.ExtOutboundSubnetId
			},
			Condition: nil,
		},
		{
			Name: "security_group_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "security_group_id",
				Description: "The security group ID to attach to the EC2 instance.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.ExtOutboundSecurityGroupId
			},
			Condition: nil,
		},
		{
			Name: "confluent_cloud_cluster_api_key",
			Definition: hcltypes.TerraformVariable{
				Name:        "confluent_cloud_cluster_api_key",
				Description: "API key of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return ""
			},
			Condition: nil,
		},
		{
			Name: "confluent_cloud_cluster_api_secret",
			Definition: hcltypes.TerraformVariable{
				Name:        "confluent_cloud_cluster_api_secret",
				Description: "API secret of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return ""
			},
			Condition: nil,
		},
		{
			Name: "target_cluster_rest_endpoint",
			Definition: hcltypes.TerraformVariable{
				Name:        "target_cluster_rest_endpoint",
				Description: "REST endpoint of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.TargetRestEndpoint
			},
			Condition: nil,
		},
		{
			Name: "target_cluster_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "target_cluster_id",
				Description: "ID of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.TargetClusterId
			},
			Condition: nil,
		},
		{
			Name: "cluster_link_name",
			Definition: hcltypes.TerraformVariable{
				Name:        "cluster_link_name",
				Description: "Name of the cluster link that will be created between the source and target Confluent Cloud clusters.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.ClusterLinkName
			},
			Condition: nil,
		},
		{
			Name: "source_cluster_id",
			Definition: hcltypes.TerraformVariable{
				Name:        "source_cluster_id",
				Description: "ID of the source Kafka cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.SourceClusterId
			},
			Condition: nil,
		},
		{
			Name: "source_cluster_bootstrap_servers",
			Definition: hcltypes.TerraformVariable{
				Name:        "source_cluster_bootstrap_servers",
				Description: "Bootstrap brokers of the source Kafka cluster that data will be migrated from.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				if request.JumpClusterAuthType == "plaintext" {
					return request.SourcePlaintextBootstrapServers
				}
				return request.SourceSaslScramBootstrapServers
			},
			Condition: nil,
		},
		{
			Name: "source_sasl_scram_mechanism",
			Definition: hcltypes.TerraformVariable{
				Name:        "source_sasl_scram_mechanism",
				Description: "The SASL/SCRAM mechanism of the source Kafka cluster (SCRAM-SHA-256 or SCRAM-SHA-512).",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request hclrequests.MigrationWizardRequest) any {
				return request.SourceSaslScramMechanism
			},
			Condition: func(request hclrequests.MigrationWizardRequest) bool {
				return request.JumpClusterAuthType != "plaintext"
			},
			FromModuleOutput: "",
		},
		{
			Name: "source_sasl_scram_username",
			Definition: hcltypes.TerraformVariable{
				Name:        "source_sasl_scram_username",
				Description: "SASL SCRAM username of the source Kafka cluster that data will be migrated from.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return ""
			},
			Condition: func(request hclrequests.MigrationWizardRequest) bool {
				return request.JumpClusterAuthType != "plaintext"
			},
		},
		{
			Name: "source_sasl_scram_password",
			Definition: hcltypes.TerraformVariable{
				Name:        "source_sasl_scram_password",
				Description: "SASL SCRAM password of the source Kafka cluster that data will be migrated from.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(_ hclrequests.MigrationWizardRequest) any {
				return ""
			},
			Condition: func(request hclrequests.MigrationWizardRequest) bool {
				return request.JumpClusterAuthType != "plaintext"
			},
		},
	}
}

func GetExternalOutboundClusterLinkingModuleVariableDefinitions(request hclrequests.MigrationWizardRequest) []hcltypes.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetExternalOutboundClusterLinkingVariables(), request)
}
