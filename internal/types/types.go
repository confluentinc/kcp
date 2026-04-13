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
	AuthTypeSASLPlain                AuthType = "SASL/PLAIN"
)

// SchemaRegistryAuthType represents the different authentication types supported by Schema Registry
type SchemaRegistryAuthType string

const (
	SchemaRegistryAuthTypeUnauthenticated SchemaRegistryAuthType = "Unauthenticated"
	SchemaRegistryAuthTypeBasicAuth       SchemaRegistryAuthType = "BasicAuth"
)

func (a AuthType) IsValid() bool {
	switch a {
	case AuthTypeSASLSCRAM, AuthTypeIAM, AuthTypeTLS, AuthTypeUnauthenticatedPlaintext, AuthTypeUnauthenticatedTLS, AuthTypeSASLPlain:
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
		string(AuthTypeSASLPlain),
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

type MigrationType int

const (
	PublicMskEndpoints                   MigrationType = 1
	ExternalOutboundClusterLink          MigrationType = 2
	ExternalOutboundClusterLinkUnauthTls MigrationType = 3
	JumpClusterSaslScram                 MigrationType = 4
	JumpClusterIam                       MigrationType = 5
)

func (m MigrationType) IsValid() bool {
	switch m {
	case PublicMskEndpoints, ExternalOutboundClusterLink, ExternalOutboundClusterLinkUnauthTls, JumpClusterSaslScram, JumpClusterIam:
		return true
	default:
		return false
	}
}

func ToMigrationType(input string) (MigrationType, error) {
	value, err := strconv.Atoi(input)
	if err != nil {
		return 0, fmt.Errorf("invalid input: must be a number")
	}
	m := MigrationType(value)
	if !m.IsValid() {
		return 0, fmt.Errorf("invalid MigrationType value: %d", value)
	}
	return m, nil
}

type Manifest struct {
	MigrationInfraType MigrationType `json:"migration_infra_type"`
}

type TargetClusterWizardRequest struct {
	AwsRegion              string   `json:"aws_region"`
	NeedsEnvironment       bool     `json:"needs_environment"`
	EnvironmentName        string   `json:"environment_name"`
	EnvironmentId          string   `json:"environment_id"`
	NeedsCluster           bool     `json:"needs_cluster"`
	ClusterName            string   `json:"cluster_name"`
	ClusterType            string   `json:"cluster_type"`
	ClusterAvailability    string   `json:"cluster_availability"` // "SINGLE_ZONE" or "MULTI_ZONE"
	ClusterCku             int      `json:"cluster_cku"`          // Number of CKUs (1+, MULTI_ZONE requires >= 2)
	NeedsPrivateLink       bool     `json:"needs_private_link"`
	UseExistingRoute53Zone bool     `json:"use_existing_route53_zone"`
	PreventDestroy         bool     `json:"prevent_destroy"`
	VpcId                  string   `json:"vpc_id"`
	SubnetCidrRanges       []string `json:"subnet_cidr_ranges"`
}

type TerraformFiles struct {
	MainTf           string            `json:"main.tf"`
	ProvidersTf      string            `json:"providers.tf"`
	VariablesTf      string            `json:"variables.tf"`
	InputsAutoTfvars string            `json:"inputs.auto.tfvars"`
	OutputsTf        string            `json:"outputs.tf"`
	PerPrincipalTf   map[string]string `json:"per_principal_tf,omitempty"`
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
	HasPublicEndpoints bool `json:"has_public_brokers"`

	VpcId string `json:"vpc_id"`

	UseJumpClusters            bool                            `json:"use_jump_clusters"`
	ExtOutboundSecurityGroupId string                          `json:"ext_outbound_security_group_id"`
	ExtOutboundSubnetId        string                          `json:"ext_outbound_subnet_id"`
	ExtOutboundBrokers         []ExtOutboundClusterKafkaBroker `json:"source_kafka_brokers"`

	ExistingPrivateLinkVpceId string `json:"existing_private_link_vpce_id"`

	HasExistingInternetGateway bool `json:"has_existing_internet_gateway"`

	JumpClusterInstanceType        string   `json:"jump_cluster_instance_type"`
	JumpClusterBrokerStorage       int      `json:"jump_cluster_broker_storage"`
	JumpClusterBrokerSubnetCidr    []string `json:"jump_cluster_broker_subnet_cidr"`
	JumpClusterSetupHostSubnetCidr string   `json:"jump_cluster_setup_host_subnet_cidr"`

	JumpClusterAuthType             string `json:"jump_cluster_auth_type"`
	SourceClusterId                 string `json:"source_cluster_id"`
	JumpClusterIamAuthRoleName      string `json:"jump_cluster_iam_auth_role_name"`
	SourceSaslScramBootstrapServers string `json:"source_sasl_scram_bootstrap_servers"`
	SourceSaslScramMechanism        string `json:"source_sasl_scram_mechanism"`
	SourceUnauthTlsBootstrapServers string `json:"source_unauth_tls_bootstrap_servers"`
	SourceSaslIamBootstrapServers   string `json:"source_sasl_iam_bootstrap_servers"`
	SourceRegion                    string `json:"source_region"`
	TargetEnvironmentId             string `json:"target_environment_id"`
	TargetClusterId                 string `json:"target_cluster_id"`
	TargetRestEndpoint              string `json:"target_rest_endpoint"`
	TargetBootstrapEndpoint         string `json:"target_bootstrap_endpoint"`
	ClusterLinkName                 string `json:"cluster_link_name"`
	TargetClusterType               string `json:"target_cluster_type"`
}

type ExtOutboundClusterKafkaBroker struct {
	ID        string                            `json:"broker_id"`
	SubnetID  string                            `json:"subnet_id"`
	Endpoints []ExtOutboundClusterKafkaEndpoint `json:"endpoints"`
}

type ExtOutboundClusterKafkaEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	IP   string `json:"ip"`
}

type MigrateAclsRequest struct {
	SelectedPrincipals        []string `json:"selected_principals"`
	TargetClusterId           string   `json:"target_cluster_id"`
	TargetClusterRestEndpoint string   `json:"target_cluster_rest_endpoint"`
	PreventDestroy            bool     `json:"prevent_destroy"`

	SourceType string `json:"source_type"`
	ClusterId  string `json:"cluster_id"`

	// This is not sent by the UI payload but instead built by the API service before being passed on to the HCL service.
	AclsByPrincipal map[string][]Acls `json:"-"`
}

type MirrorTopicsRequest struct {
	SelectedTopics            []string `json:"selected_topics"`
	ClusterLinkName           string   `json:"cluster_link_name"`
	TargetClusterId           string   `json:"target_cluster_id"`
	TargetClusterRestEndpoint string   `json:"target_cluster_rest_endpoint"`
}

type ReverseProxyRequest struct {
	Region                                 string `json:"region"`
	VPCId                                  string `json:"vpc_id"`
	PublicSubnetCidr                       string `json:"public_subnet_cidr"`
	ConfluentCloudClusterBootstrapEndpoint string `json:"confluent_cloud_cluster_bootstrap_endpoint"`
}

type MigrateSchemasRequest struct {
	ConfluentCloudSchemaRegistryURL string                         `json:"confluent_cloud_schema_registry_url"`
	SchemaRegistries                []SchemaRegistryExporterConfig `json:"schema_registries"`
}

type SchemaRegistryExporterConfig struct {
	Migrate   bool     `json:"migrate"`
	Subjects  []string `json:"subjects"`
	SourceURL string   `json:"source_url"`
}

type MigrateGlueSchemasRequest struct {
	ConfluentCloudSchemaRegistryURL string                              `json:"confluent_cloud_schema_registry_url"`
	GlueRegistries                  []GlueSchemaRegistryMigrationConfig `json:"glue_registries"`
}

type GlueSchemaRegistryMigrationConfig struct {
	Migrate      bool         `json:"migrate"`
	RegistryName string       `json:"registry_name"`
	Region       string       `json:"region"`
	Schemas      []GlueSchema `json:"schemas"`
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
	OutputsTf        string                          `json:"outputs.tf"`
	ReadmeMd         string                          `json:"README.md"`
	InputsAutoTfvars string                          `json:"inputs.auto.tfvars"`
	Modules          []MigrationInfraTerraformModule `json:"modules"`
}

// MigrationScriptsTerraformProject represents the complete Terraform configuration for migration scripts
type MigrationScriptsTerraformProject struct {
	// not really a module, but its the same structure
	Folders []MigrationScriptsTerraformFolder `json:"modules"`
}

// MigrationScriptsTerraformFolder represents a Terraform folder within the migration scripts
type MigrationScriptsTerraformFolder struct {
	Name             string            `json:"name"`
	MainTf           string            `json:"main.tf"`
	ProvidersTf      string            `json:"providers.tf"`
	VariablesTf      string            `json:"variables.tf"`
	InputsAutoTfvars string            `json:"inputs.auto.tfvars"`
	AdditionalFiles  map[string]string `json:"additional_files,omitempty"`
}
