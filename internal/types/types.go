package types

import (
	"fmt"
	"strconv"
)

type TerraformState struct {
	Outputs TerraformOutputOld `json:"outputs"`
}

// a type for the output.json file in the target_env folder
// NOTE: This will be deprecated once we are completely on the HCL-servrice based approach.
type TerraformOutputOld struct {
	ConfluentCloudClusterApiKey                TerraformOutputValue `json:"confluent_cloud_cluster_api_key"`
	ConfluentCloudClusterApiKeySecret          TerraformOutputValue `json:"confluent_cloud_cluster_api_key_secret"`
	ConfluentCloudClusterId                    TerraformOutputValue `json:"confluent_cloud_cluster_id"`
	ConfluentCloudClusterRestEndpoint          TerraformOutputValue `json:"confluent_cloud_cluster_rest_endpoint"`
	ConfluentCloudClusterBootstrapEndpoint     TerraformOutputValue `json:"confluent_cloud_cluster_bootstrap_endpoint"`
	ConfluentPlatformControllerBootstrapServer TerraformOutputValue `json:"confluent_platform_controller_bootstrap_server"`
}

type TerraformOutputValue struct {
	Sensitive bool   `json:"sensitive"`
	Type      string `json:"type"`
	Value     any    `json:"value"`
}

// AuthType represents the different authentication types supported by MSK clusters
type AuthType string

const (
	AuthTypeSASLSCRAM                AuthType = "SASL/SCRAM"
	AuthTypeIAM                      AuthType = "SASL/IAM"
	AuthTypeTLS                      AuthType = "TLS"
	AuthTypeUnauthenticatedPlaintext AuthType = "Unauthenticated (Plaintext)"
	AuthTypeUnauthenticatedTLS       AuthType = "Unauthenticated (TLS Encryption)"
)

// SchemaRegistryAuthType represents the different authentication types supported by Schema Registry
type SchemaRegistryAuthType string

const (
	SchemaRegistryAuthTypeUnauthenticated SchemaRegistryAuthType = "Unauthenticated"
	SchemaRegistryAuthTypeBasicAuth       SchemaRegistryAuthType = "BasicAuth"
)

func (a AuthType) IsValid() bool {
	switch a {
	case AuthTypeSASLSCRAM, AuthTypeIAM, AuthTypeTLS, AuthTypeUnauthenticatedPlaintext, AuthTypeUnauthenticatedTLS:
		return true
	default:
		return false
	}
}

// Values returns all possible AuthType values as strings
func (a AuthType) Values() []string {
	return AllAuthTypes()
}

// AllAuthTypes returns all possible AuthType values as strings
// This can be called statically without needing an AuthType instance
func AllAuthTypes() []string {
	return []string{
		string(AuthTypeSASLSCRAM),
		string(AuthTypeIAM),
		string(AuthTypeTLS),
		string(AuthTypeUnauthenticatedPlaintext),
		string(AuthTypeUnauthenticatedTLS),
	}
}

type ConnectAuthMethod string

const (
	ConnectAuthMethodSaslScram       ConnectAuthMethod = "SASL/SCRAM"
	ConnectAuthMethodTls             ConnectAuthMethod = "TLS"
	ConnectAuthMethodUnauthenticated ConnectAuthMethod = "Unauthenticated"
)

type ConnectSaslScramAuth struct {
	Username string
	Password string
}

type ConnectTlsAuth struct {
	CACert     string
	ClientCert string
	ClientKey  string
}

type MigrationInfraType int

const (
	MskCpCcPrivateSaslIam   MigrationInfraType = 1 // MSK to CP to CC Private with SASL/IAM
	MskCpCcPrivateSaslScram MigrationInfraType = 2 // MSK to CP to CC Private with SASL/SCRAM
	MskCcPublic             MigrationInfraType = 3 // MSK to CC Public
)

func (m MigrationInfraType) IsValid() bool {
	switch m {
	case MskCpCcPrivateSaslIam, MskCpCcPrivateSaslScram, MskCcPublic:
		return true
	default:
		return false
	}
}

func ToMigrationInfraType(input string) (MigrationInfraType, error) {
	value, err := strconv.Atoi(input)
	if err != nil {
		return 0, fmt.Errorf("invalid input: must be a number")
	}
	m := MigrationInfraType(value)
	if !m.IsValid() {
		return 0, fmt.Errorf("invalid MigrationInfraType value: %d", value)
	}
	return m, nil
}

type Manifest struct {
	MigrationInfraType MigrationInfraType `json:"migration_infra_type"`
}

type TargetClusterWizardRequest struct {
	AwsRegion        string   `json:"aws_region"`
	NeedsEnvironment bool     `json:"needs_environment"`
	EnvironmentName  string   `json:"environment_name"`
	EnvironmentId    string   `json:"environment_id"`
	NeedsCluster     bool     `json:"needs_cluster"`
	ClusterName      string   `json:"cluster_name"`
	ClusterType      string   `json:"cluster_type"`
	NeedsPrivateLink bool     `json:"needs_private_link"`
	VpcId            string   `json:"vpc_id"`
	SubnetCidrRanges []string `json:"subnet_cidr_ranges"`
}

type TerraformFiles struct {
	MainTf           string `json:"main.tf"`
	ProvidersTf      string `json:"providers.tf"`
	VariablesTf      string `json:"variables.tf"`
	InputsAutoTfvars string `json:"inputs.auto.tfvars"`
	OutputsTf        string `json:"outputs.tf"`
}

type TerraformVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive"`
	Type        string `json:"type"`
}

type TerraformOutput struct {
	Name        string
	Description string
	Sensitive   bool
	Value       string
}

type MigrationWizardRequest struct {
	HasPublicCcEndpoints bool `json:"has_public_cc_endpoints"`

	VpcId string `json:"vpc_id"`

	HasExistingPrivateLink       bool     `json:"has_existing_private_link"`
	ReuseExistingSubnets         bool     `json:"reuse_existing_subnets"`
	PrivateLinkExistingSubnetIds []string `json:"private_link_existing_subnet_ids"`
	PrivateLinkNewSubnetsCidr    []string `json:"private_link_new_subnets_cidr"`

	HasExistingInternetGateway bool `json:"has_existing_internet_gateway"`

	JumpClusterInstanceType        string   `json:"jump_cluster_instance_type"`
	JumpClusterBrokerStorage       int      `json:"jump_cluster_broker_storage"`
	JumpClusterBrokerSubnetCidr    []string `json:"jump_cluster_broker_subnet_cidr"`
	JumpClusterSetupHostSubnetCidr string   `json:"jump_cluster_setup_host_subnet_cidr"`

	MskJumpClusterAuthType       string `json:"msk_jump_cluster_auth_type"`
	MskClusterId                 string `json:"msk_cluster_id"`
	JumpClusterIamAuthRoleName   string `json:"jump_cluster_iam_auth_role_name"`
	MskSaslScramBootstrapServers string `json:"msk_sasl_scram_bootstrap_servers"`
	MskSaslIamBootstrapServers   string `json:"msk_sasl_iam_bootstrap_servers"`
	MskRegion                    string `json:"msk_region"`
	TargetEnvironmentId          string `json:"target_environment_id"`
	TargetClusterId              string `json:"target_cluster_id"`
	TargetRestEndpoint           string `json:"target_rest_endpoint"`
	TargetBootstrapEndpoint      string `json:"target_bootstrap_endpoint"`
	ClusterLinkName              string `json:"cluster_link_name"`
}

type MigrateTopicsRequest struct {
	MigrationType                     string   `json:"migration_type"`
	SelectedTopics                    []string `json:"selected_topics"`
	ClusterLinkName                   string   `json:"cluster_link_name"`
	ConfluentCloudClusterId           string   `json:"confluent_cloud_cluster_id"`
	ConfluentCloudClusterRestEndpoint string   `json:"confluent_cloud_cluster_rest_endpoint"`
}

type MigrateSchemasRequestOLD struct {
	SourceSchemaRegistryURL string     `json:"source_schema_registry_url"`
	Exporters               []Exporter `json:"exporters"`
}

type Exporter struct {
	Name        string   `json:"name"`
	ContextType string   `json:"context_type"`
	ContextName string   `json:"context_name"`
	Subjects    []string `json:"subjects"`
}

type MigrateSchemasRequest struct {
	SchemaRegistries []SchemaRegistryExporter `json:"schema_registries"`
}

type SchemaRegistryExporter struct {
	Id          string   `json:"id"`
	ContextType string   `json:"context_type" default:"NONE"`
	Enabled     bool     `json:"enabled"`
	Subjects    []string `json:"subjects"`
	Url         string   `json:"url"`
}

// MigrationInfraTerraformModule represents a Terraform module within the migration infrastructure
// configuration. Each module contains its own Terraform files and additional assets.
type MigrationInfraTerraformModule struct {
	Name            string            `json:"name"`
	MainTf          string            `json:"main.tf"`
	VariablesTf     string            `json:"variables.tf"`
	OutputsTf       string            `json:"outputs.tf"`
	VersionsTf      string            `json:"versions.tf"`
	AdditionalFiles map[string]string `json:"additional_files"`
}

// MigrationInfraTerraformProject represents the complete Terraform configuration for migration
// infrastructure. "project" = root config + modules
type MigrationInfraTerraformProject struct {
	MainTf           string                          `json:"main.tf"`
	ProvidersTf      string                          `json:"providers.tf"`
	VariablesTf      string                          `json:"variables.tf"`
	InputsAutoTfvars string                          `json:"inputs.auto.tfvars"`
	Modules          []MigrationInfraTerraformModule `json:"modules"`
}
