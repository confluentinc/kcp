package modules

import "github.com/confluentinc/kcp/internal/types"

// VariableSchema defines the metadata for a Terraform variable (name, type, description, sensitivity).
// Shared across modules to avoid duplicating variable definitions.
type VariableSchema struct {
	Name        string
	Type        string
	Description string
	Sensitive   bool
}

// ToDefinition converts a VariableSchema to a types.TerraformVariable.
func (v VariableSchema) ToDefinition() types.TerraformVariable {
	return types.TerraformVariable{
		Name:        v.Name,
		Type:        v.Type,
		Description: v.Description,
		Sensitive:   v.Sensitive,
	}
}

// Shared variable schemas — each variable defined exactly once.

// Provider variables
var (
	SchemaConfluentCloudAPIKey = VariableSchema{
		Name: "confluent_cloud_api_key", Type: "string",
		Description: "Confluent Cloud API Key", Sensitive: false,
	}
	SchemaConfluentCloudAPISecret = VariableSchema{
		Name: "confluent_cloud_api_secret", Type: "string",
		Description: "Confluent Cloud API Secret", Sensitive: true,
	}
	SchemaAWSRegion = VariableSchema{
		Name: "aws_region", Type: "string",
		Description: "The AWS region", Sensitive: false,
	}
)

// Cluster credential variables
var (
	SchemaConfluentCloudClusterAPIKey = VariableSchema{
		Name: "confluent_cloud_cluster_api_key", Type: "string",
		Description: "Confluent Cloud cluster API key", Sensitive: false,
	}
	SchemaConfluentCloudClusterAPISecret = VariableSchema{
		Name: "confluent_cloud_cluster_api_secret", Type: "string",
		Description: "Confluent Cloud cluster API secret", Sensitive: true,
	}
)

// MSK authentication variables
var (
	SchemaMSKSaslScramUsername = VariableSchema{
		Name: "msk_sasl_scram_username", Type: "string",
		Description: "MSK SASL SCRAM Username", Sensitive: false,
	}
	SchemaMSKSaslScramPassword = VariableSchema{
		Name: "msk_sasl_scram_password", Type: "string",
		Description: "MSK SASL SCRAM Password", Sensitive: true,
	}
)

// MSK bootstrap variables
var (
	SchemaMSKSaslScramBootstrapServers = VariableSchema{
		Name: "msk_sasl_scram_bootstrap_servers", Type: "string",
		Description: "The SASL/SCRAM bootstrap servers of the source MSK cluster that data will be migrated from.", Sensitive: false,
	}
)

// Cluster link variables
var (
	SchemaTargetClusterRestEndpoint = VariableSchema{
		Name: "target_cluster_rest_endpoint", Type: "string",
		Description: "The REST endpoint of the target Confluent Cloud cluster that data will be migrated to.", Sensitive: false,
	}
	SchemaTargetClusterID = VariableSchema{
		Name: "target_cluster_id", Type: "string",
		Description: "The ID of the target Confluent Cloud cluster that data will be migrated to.", Sensitive: false,
	}
	SchemaClusterLinkName = VariableSchema{
		Name: "cluster_link_name", Type: "string",
		Description: "The name of the cluster link that will be created between the source and target Confluent Cloud clusters.", Sensitive: false,
	}
	SchemaMSKClusterID = VariableSchema{
		Name: "msk_cluster_id", Type: "string",
		Description: "The ID of the source MSK cluster that data will be migrated from.", Sensitive: false,
	}
)

// Network variables
var (
	SchemaVpcID = VariableSchema{
		Name: "vpc_id", Type: "string",
		Description: "VPC ID", Sensitive: false,
	}
)

// Jump cluster variables
var (
	SchemaJumpClusterSecurityGroupIDs = VariableSchema{
		Name: "jump_cluster_security_group_ids", Type: "string",
		Description: "Security Group IDs for the Jump Cluster", Sensitive: false,
	}
	SchemaJumpClusterSSHKeyPairName = VariableSchema{
		Name: "jump_cluster_ssh_key_pair_name", Type: "string",
		Description: "SSH key pair name for the Jump Cluster", Sensitive: false,
	}
)

// ExtractModuleVariableDefinitions extracts variable definitions for a module.
// Unlike extractVariableDefinitions (which is for root-level), this includes ALL variables
// regardless of ValueExtractor or FromModuleOutput — a module needs all its declared variables.
func ExtractModuleVariableDefinitions[R any](allVars []ModuleVariable[R], request R) []types.TerraformVariable {
	var definitions []types.TerraformVariable

	for _, varDef := range allVars {
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}
