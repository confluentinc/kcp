package confluent

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/generators/hcl"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// Generator handles Confluent Cloud Terraform generation
type Generator struct {
	// Configuration fields if needed in the future
}

// Config holds the configuration for generating Confluent Cloud Terraform files
type Config struct {
	NeedsEnvironment bool
	EnvironmentName  string
	EnvironmentId    string
	NeedsCluster     bool
	ClusterName      string
	ClusterType      string
}

// NewGenerator creates a new Confluent generator
func NewGenerator() *Generator {
	return &Generator{}
}

// GenerateMainTf generates the main.tf file content using individual resource functions
func (g *Generator) GenerateMainTf(cfg Config) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Add environment (create or use data source)
	if cfg.NeedsEnvironment {
		rootBody.AppendBlock(GenerateEnvironmentResource(cfg.EnvironmentName))
		rootBody.AppendNewline()
	} else {
		rootBody.AppendBlock(GenerateEnvironmentDataSource(cfg.EnvironmentId))
		rootBody.AppendNewline()
	}

	// Add Kafka cluster (create if needed)
	if cfg.NeedsCluster || cfg.NeedsEnvironment {
		rootBody.AppendBlock(GenerateKafkaClusterResource(cfg.ClusterName, cfg.ClusterType, "us-east-1", cfg.NeedsEnvironment))
		rootBody.AppendNewline()
	}

	// Add Schema Registry data source
	rootBody.AppendBlock(GenerateSchemaRegistryDataSource(cfg.NeedsEnvironment))
	rootBody.AppendNewline()

	// Add Service Account
	description := fmt.Sprintf("Service account to manage the %s environment.", cfg.EnvironmentName)
	rootBody.AppendBlock(GenerateServiceAccount("app-manager", description))
	rootBody.AppendNewline()

	// Add Role Bindings
	rootBody.AppendBlock(GenerateRoleBinding(
		"subject-resource-owner",
		"User:${confluent_service_account.app-manager.id}",
		"ResourceOwner",
		hcl.TokensForStringTemplate("${data.confluent_schema_registry_cluster.schema_registry.resource_name}/subject=*"),
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(GenerateRoleBinding(
		"app-manager-kafka-cluster-admin",
		"User:${confluent_service_account.app-manager.id}",
		"CloudClusterAdmin",
		hcl.TokensForResourceReference("confluent_kafka_cluster.cluster.rbac_crn"),
	))
	rootBody.AppendNewline()

	envResourceName := GetEnvironmentResourceName(cfg.NeedsEnvironment)
	rootBody.AppendBlock(GenerateRoleBinding(
		"app-manager-kafka-data-steward",
		"User:${confluent_service_account.app-manager.id}",
		"DataSteward",
		hcl.TokensForResourceReference(envResourceName),
	))
	rootBody.AppendNewline()

	// Add Kafka ACLs
	rootBody.AppendBlock(GenerateKafkaACL(
		"app-manager-create-on-cluster",
		"CLUSTER",
		"kafka-cluster",
		"LITERAL",
		"User:${confluent_service_account.app-manager.id}",
		"CREATE",
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(GenerateKafkaACL(
		"app-manager-describe-on-cluster",
		"CLUSTER",
		"kafka-cluster",
		"LITERAL",
		"User:${confluent_service_account.app-manager.id}",
		"DESCRIBE",
	))
	rootBody.AppendNewline()

	rootBody.AppendBlock(GenerateKafkaACL(
		"app-manager-read-all-consumer-groups",
		"GROUP",
		"*",
		"PREFIXED",
		"User:${confluent_service_account.app-manager.id}",
		"READ",
	))
	rootBody.AppendNewline()

	// Add API Keys
	rootBody.AppendBlock(GenerateSchemaRegistryAPIKey(cfg.EnvironmentName, cfg.NeedsEnvironment))
	rootBody.AppendNewline()

	rootBody.AppendBlock(GenerateKafkaAPIKey(cfg.EnvironmentName, cfg.NeedsEnvironment))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// GenerateProvidersTf generates the providers.tf file content
func (g *Generator) GenerateProvidersTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Terraform block
	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	// Required providers block
	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	// Confluent provider
	requiredProvidersBody.SetAttributeValue("confluent", cty.ObjectVal(map[string]cty.Value{
		"source":  cty.StringVal("confluentinc/confluent"),
		"version": cty.StringVal("2.50.0"),
	}))

	rootBody.AppendNewline()

	// Provider block
	providerBlock := rootBody.AppendNewBlock("provider", []string{"confluent"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeRaw("cloud_api_key", hcl.TokensForResourceReference("var.confluent_cloud_api_key"))
	providerBody.SetAttributeRaw("cloud_api_secret", hcl.TokensForResourceReference("var.confluent_cloud_api_secret"))

	return string(f.Bytes())
}

// GenerateVariablesTf generates the variables.tf file content
func (g *Generator) GenerateVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Define base variables
	variables := []struct {
		name        string
		description string
		sensitive   bool
	}{
		{"confluent_cloud_api_key", "Confluent Cloud API Key", true},
		{"confluent_cloud_api_secret", "Confluent Cloud API Secret", true},
	}

	for _, v := range variables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeRaw("type", hcl.TokensForResourceReference("string"))
		if v.description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.description))
		}
		if v.sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}
