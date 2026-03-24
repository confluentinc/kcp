package modules

import "github.com/confluentinc/kcp/internal/types"

func GetJumpClusterVariables() []ModuleVariable[types.MigrationWizardRequest] {
	return []ModuleVariable[types.MigrationWizardRequest]{
		{
			Name: "jump_cluster_broker_subnet_ids",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_broker_subnet_ids",
				Description: "IDs of the subnets that the jump cluster broker instances are deployed to.",
				Sensitive:   false,
				Type:        "list(string)",
			},
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return []string{} // Retrieved from networking module output.
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "jump_cluster_instance_type",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_instance_type",
				Description: "Instance type of the jump cluster instances.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.JumpClusterInstanceType
			},
			Condition: nil,
		},
		{
			Name:       SchemaJumpClusterSecurityGroupIDs.Name,
			Definition: SchemaJumpClusterSecurityGroupIDs.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return []string{} // Retrieved from networking module output.
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name:       SchemaJumpClusterSSHKeyPairName.Name,
			Definition: SchemaJumpClusterSSHKeyPairName.ToDefinition(),
			ValueExtractor: func(_ types.MigrationWizardRequest) any {
				return "" // Retrieved from networking module output.
			},
			Condition:        nil,
			FromModuleOutput: "networking",
		},
		{
			Name: "jump_cluster_iam_auth_role_name",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_iam_auth_role_name",
				Description: "Name of the IAM role that will be attached to the jump cluster instances to enable IAM authenticated cluster linking between MSK and jump cluster.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.JumpClusterIamAuthRoleName
			},
			Condition: func(request types.MigrationWizardRequest) bool {
				return request.MskJumpClusterAuthType == "iam"
			},
		},
		{
			Name: "jump_cluster_broker_storage",
			Definition: types.TerraformVariable{
				Name:        "jump_cluster_broker_storage",
				Description: "Storage size of the jump cluster broker instances.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.JumpClusterBrokerStorage
			},
			Condition: nil,
		},
		{
			Name: "confluent_cloud_cluster_id",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_id",
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
			Name: "confluent_cloud_cluster_bootstrap_endpoint",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_bootstrap_endpoint",
				Description: "Bootstrap endpoint of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.TargetBootstrapEndpoint
			},
			Condition: nil,
		},
		{
			Name: "confluent_cloud_cluster_rest_endpoint",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_rest_endpoint",
				Description: "REST endpoint of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.TargetRestEndpoint
			},
			Condition: nil,
		},
		// Needs to be passed to the module as it is used for creating the cluster link between the jump cluster and Confluent Cloud.
		{
			Name: "confluent_cloud_cluster_api_key",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_api_key",
				Description: "API key of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return "" // User prompted for value at Terraform apply.
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "confluent_cloud_cluster_api_secret",
			Definition: types.TerraformVariable{
				Name:        "confluent_cloud_cluster_api_secret",
				Description: "API secret of the Confluent Cloud cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return "" // User prompted for value at Terraform apply.
			},
			Condition:        nil,
			FromModuleOutput: "",
		},
		{
			Name: "msk_cluster_id",
			Definition: types.TerraformVariable{
				Name:        "msk_cluster_id",
				Description: "ID of the MSK cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.MskClusterId
			},
			Condition: nil,
		},
		{
			Name: "msk_cluster_bootstrap_brokers",
			Definition: types.TerraformVariable{
				Name:        "msk_cluster_bootstrap_brokers",
				Description: "Bootstrap brokers of the MSK cluster that data will be migrated to.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				switch request.MskJumpClusterAuthType {
				case "sasl_scram":
					return request.MskSaslScramBootstrapServers
				case "unauth_tls":
					return request.MskUnauthTlsBootstrapServers
				default:
					return request.MskSaslIamBootstrapServers
				}
			},
			Condition: nil,
		},
		{
			Name: "msk_sasl_scram_username",
			Definition: types.TerraformVariable{
				Name:        "msk_sasl_scram_username",
				Description: "SASL SCRAM username of the MSK cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return "" // User prompted for value at Terraform apply.
			},
			Condition: func(request types.MigrationWizardRequest) bool {
				return request.MskJumpClusterAuthType == "sasl_scram"
			},
		},
		{
			Name: "msk_sasl_scram_password",
			Definition: types.TerraformVariable{
				Name:        "msk_sasl_scram_password",
				Description: "SASL SCRAM password of the MSK cluster that data will be migrated to.",
				Sensitive:   true,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return "" // User prompted for value at Terraform apply.
			},
			Condition: func(request types.MigrationWizardRequest) bool {
				return request.MskJumpClusterAuthType == "sasl_scram"
			},
		},
		{
			Name: "cluster_link_name",
			Definition: types.TerraformVariable{
				Name:        "cluster_link_name",
				Description: "Name of the cluster links that will be created between MSK and Confluent Cloud through the jump cluster.",
				Sensitive:   false,
				Type:        "string",
			},
			ValueExtractor: func(request types.MigrationWizardRequest) any {
				return request.ClusterLinkName
			},
			Condition: nil,
		},
	}
}

func GetJumpClusterModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	return ExtractModuleVariableDefinitions(GetJumpClusterVariables(), request)
}

var JumpClusterModuleOutputs = []types.TerraformOutput{
	{
		Name:        "jump_cluster_instances_private_dns",
		Description: "Private DNS addresses of the jump cluster instances.",
		Sensitive:   false,
		Value:       "values(aws_instance.jump_cluster)[*].private_dns",
	},
}

func GetJumpClusterModuleOutputDefinitions() []types.TerraformOutput {
	return JumpClusterModuleOutputs
}
