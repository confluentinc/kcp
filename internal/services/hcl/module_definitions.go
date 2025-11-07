package hcl

import (
	"github.com/confluentinc/kcp/internal/types"
)

// TerraformOutput represents a Terraform output definition
type TerraformOutput struct {
	// Name is the Terraform output name (e.g., "public_subnet_id")
	Name string
	// Description is an optional description for the output
	Description string
	// Sensitive indicates if the output value should be marked as sensitive
	Sensitive bool
	// Value is the Terraform HCL expression for the output value
	Value string
}

// ModuleVariableDefinition represents a Terraform variable definition and its value mapping
type ModuleVariableDefinition struct {
	// Name is the Terraform variable name (e.g., "vpc_id")
	Name string
	// Definition is the Terraform variable definition
	Definition types.TerraformVariable
	// ValueExtractor extracts the value from MigrationWizardRequest
	ValueExtractor func(request types.MigrationWizardRequest) interface{}
	// Condition determines if this variable should be included (nil means always include)
	Condition func(request types.MigrationWizardRequest) bool
}

// ModuleOutputDefinition represents a Terraform output definition
type ModuleOutputDefinition struct {
	// Name is the Terraform output name (e.g., "public_subnet_id")
	Name string
	// Definition is the Terraform output definition
	Definition TerraformOutput
}

// MigrationInfraModuleVariables defines all variables used in migration infrastructure modules
// This includes root-level variables (providers) and module-level variables
var MigrationInfraModuleVariables = []ModuleVariableDefinition{
	// Provider Variables
	{
		Name: "confluent_cloud_api_key",
		Definition: types.TerraformVariable{
			Name:        "confluent_cloud_api_key",
			Description: "Confluent Cloud API Key",
			Sensitive:   false,
			Type:        "string",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return "" // Provider variables are typically set via environment or manually
		},
		Condition: nil,
	},
	{
		Name: "confluent_cloud_api_secret",
		Definition: types.TerraformVariable{
			Name:        "confluent_cloud_api_secret",
			Description: "Confluent Cloud API Secret",
			Sensitive:   true,
			Type:        "string",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return "" // Provider variables are typically set via environment or manually
		},
		Condition: nil,
	},
	{
		Name: "aws_region",
		Definition: types.TerraformVariable{
			Name:        "aws_region",
			Description: "The AWS region",
			Sensitive:   false,
			Type:        "string",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return request.MskRegion // Use MSK region as AWS region
		},
		Condition: nil,
	},
	// Networking Module Variables
	{
		Name: "vpc_id",
		Definition: types.TerraformVariable{
			Name:        "vpc_id",
			Description: "ID of the VPC",
			Sensitive:   false,
			Type:        "string",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return request.VpcId
		},
		Condition: nil,
	},
	{
		Name: "jump_cluster_broker_subnet_cidrs",
		Definition: types.TerraformVariable{
			Name:        "jump_cluster_broker_subnet_cidrs",
			Description: "CIDR ranges of the jump cluster broker subnets",
			Sensitive:   false,
			Type:        "list(string)",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return request.JumpClusterBrokerSubnetCidr
		},
		Condition: nil,
	},
	{
		Name: "jump_cluster_setup_host_subnet_cidr",
		Definition: types.TerraformVariable{
			Name:        "jump_cluster_setup_host_subnet_cidr",
			Description: "CIDR block of the jump cluster setup host subnet",
			Sensitive:   false,
			Type:        "string",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return request.JumpClusterSetupHostSubnetCidr
		},
		Condition: nil,
	},
	// Jump Cluster Setup Host Module Variables (these reference module outputs)
	{
		Name: "aws_public_subnet_id",
		Definition: types.TerraformVariable{
			Name:        "aws_public_subnet_id",
			Description: "ID of the public subnet for the Ansible control node instance",
			Sensitive:   false,
			Type:        "string",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return "" // This comes from networking module output
		},
		Condition: nil,
	},
	{
		Name: "security_group_ids",
		Definition: types.TerraformVariable{
			Name:        "security_group_ids",
			Description: "IDs of the security groups for the Ansible control node instance",
			Sensitive:   false,
			Type:        "list(string)",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return []string{} // This comes from networking module output
		},
		Condition: nil,
	},
	{
		Name: "aws_key_pair_name",
		Definition: types.TerraformVariable{
			Name:        "aws_key_pair_name",
			Description: "Name of the AWS key pair for SSH access to the Ansible control node instance",
			Sensitive:   false,
			Type:        "string",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return "" // This comes from networking module output
		},
		Condition: nil,
	},
	{
		Name: "confluent_platform_broker_instances_private_dns",
		Definition: types.TerraformVariable{
			Name:        "confluent_platform_broker_instances_private_dns",
			Description: "Private DNS names of the Confluent Platform broker instances",
			Sensitive:   false,
			Type:        "list(string)",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return []string{} // This comes from confluent_platform_broker_instances module output
		},
		Condition: nil,
	},
	{
		Name: "private_key",
		Definition: types.TerraformVariable{
			Name:        "private_key",
			Description: "Private SSH key for accessing the Confluent Platform broker instances",
			Sensitive:   true,
			Type:        "string",
		},
		ValueExtractor: func(request types.MigrationWizardRequest) interface{} {
			return "" // This comes from networking module output
		},
		Condition: nil,
	},
}

// GetModuleVariableDefinitions returns variable definitions filtered by conditions
func GetModuleVariableDefinitions(request types.MigrationWizardRequest) []types.TerraformVariable {
	var definitions []types.TerraformVariable

	for _, varDef := range MigrationInfraModuleVariables {
		// Check condition if present
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		definitions = append(definitions, varDef.Definition)
	}

	return definitions
}

// GetModuleVariableValues returns a map of variable names to their values from the request
func GetModuleVariableValues(request types.MigrationWizardRequest) map[string]interface{} {
	values := make(map[string]interface{})

	for _, varDef := range MigrationInfraModuleVariables {
		// Check condition if present
		if varDef.Condition != nil && !varDef.Condition(request) {
			continue
		}
		values[varDef.Name] = varDef.ValueExtractor(request)
	}

	return values
}

// GetModuleVariableName returns the variable name for a given variable definition
// This is useful for getting the variable name constant for use in generator functions
func GetModuleVariableName(varName string) string {
	for _, varDef := range MigrationInfraModuleVariables {
		if varDef.Name == varName {
			return varDef.Name
		}
	}
	return varName
}

// GetJumpClusterSetupHostVariableDefinitions returns variable definitions for the jump cluster setup host module
// These variables reference outputs from other modules (networking, confluent_platform_broker_instances)
func GetJumpClusterSetupHostVariableDefinitions() []types.TerraformVariable {
	jumpClusterVarNames := []string{
		"aws_public_subnet_id",
		"security_group_ids",
		"aws_key_pair_name",
		"confluent_platform_broker_instances_private_dns",
		"private_key",
	}

	var definitions []types.TerraformVariable
	for _, varDef := range MigrationInfraModuleVariables {
		for _, name := range jumpClusterVarNames {
			if varDef.Name == name {
				definitions = append(definitions, varDef.Definition)
				break
			}
		}
	}

	return definitions
}

// NetworkingModuleOutputs defines all outputs for the networking module
var NetworkingModuleOutputs = []ModuleOutputDefinition{
	{
		Name: "jump_cluster_setup_host_subnet_id",
		Definition: TerraformOutput{
			Name:        "jump_cluster_setup_host_subnet_id",
			Description: "ID of the subnet that the Ansible jump cluster setup host instance is deployed to.",
			Sensitive:   false,
			Value:       "aws_subnet.jump_cluster_setup_host_subnet.id",
		},
	},
	{
		Name: "jump_cluster_broker_subnet_ids",
		Definition: TerraformOutput{
			Name:        "jump_cluster_broker_subnet_ids",
			Description: "IDs of the subnets that the jump cluster broker instances are deployed to.",
			Sensitive:   false,
			Value:       "values(aws_subnet.jump_cluster_broker_subnets)[*].id",
		},
	},
	{
		Name: "jump_cluster_ssh_key_pair_name",
		Definition: TerraformOutput{
			Name:        "jump_cluster_ssh_key_pair_name",
			Description: "Name of the AWS key pair for the jump cluster (including setup host) instances.",
			Sensitive:   false,
			Value:       "aws_key_pair.jump_cluster_ssh_key.key_name",
		},
	},
	{
		Name: "private_key",
		Definition: TerraformOutput{
			Name:        "private_key",
			Description: "Private SSH key for accessing the jump cluster (including setup host) instances.",
			Sensitive:   true,
			Value:       "tls_private_key.jump_cluster_ssh_key.private_key_pem",
		},
	},
}

// GetNetworkingModuleOutputDefinitions returns all output definitions for the networking module
func GetNetworkingModuleOutputDefinitions() []TerraformOutput {
	var definitions []TerraformOutput

	for _, outputDef := range NetworkingModuleOutputs {
		definitions = append(definitions, outputDef.Definition)
	}

	return definitions
}
