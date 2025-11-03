package types

import (
	"fmt"
	"strconv"
)

type TerraformState struct {
	Outputs TerraformOutput `json:"outputs"`
}

// a type for the output.json file in the target_env folder
type TerraformOutput struct {
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
	Region           string   `json:"region"`
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
	MainTf      string `json:"main_tf"`
	ProvidersTf string `json:"providers_tf"`
	VariablesTf string `json:"variables_tf"`
}

type TerraformVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive"`
}

type MigrationWizardRequest struct {
	// old
	// MskPubliclyAccessible        bool   `json:"msk_publicly_accessible"`
	// AuthenticationMethod         string `json:"authentication_method"`
	// TargetClusterType            string `json:"target_cluster_type"`
	// TargetEnvironmentId          string `json:"target_environment_id"`
	// TargetClusterId              string `json:"target_cluster_id"`
	// TargetRestEndpoint           string `json:"target_rest_endpoint"`
	// MskSaslScramBootstrapServers string `json:"msk_sasl_scram_bootstrap_servers"`

	// new
	HasPublicCCEndpoints bool   `json:"has_public_cc_endpoints"`
	ClusterLinkName      string `json:"cluster_link_name"`

	UseExistingSubnets bool     `json:"use_existing_subnets"`
	ExistingSubnetIds  []string `json:"existing_subnet_ids"`
	MskVPCId           string   `json:"msk_vpc_id"`
	SubnetCidrRanges   string   `json:"subnet_cidr_range"`

	UseExistingInternetGateway bool `json:"use_existing_internet_gateway"`

	BrokerType                 string `json:"broker_type"`
	BrokerAmount               string `json:"broker_amount"`
	BrokerStorageSize          string `json:"broker_storage_size"`
	JumpClusterSubnetCidrRange string `json:"jump_cluster_subnet_cidr_range"`
	AnsibleSubnetCidrRange     string `json:"ansible_subnet_cidr_range"`
	AuthenticationMethod       string `json:"authentication_method"`

	TargetEnvironmentId    string `json:"target_environment_id"`
	TargetClusterId        string `json:"target_cluster_id"`
	TargetRestEndpoint     string `json:"target_rest_endpoint"`

	MskSaslScramBootstrapServers string `json:"msk_sasl_scram_bootstrap_servers"`
	MskClusterId string `json:"msk_cluster_id"`
}

type MigrateTopicsRequest struct {
	MigrationType                     string   `json:"migration_type"`
	SelectedTopics                    []string `json:"selected_topics"`
	ClusterLinkName                   string   `json:"cluster_link_name"`
	ConfluentCloudClusterId           string   `json:"confluent_cloud_cluster_id"`
	ConfluentCloudClusterRestEndpoint string   `json:"confluent_cloud_cluster_rest_endpoint"`
}

type MigrateSchemasRequest struct {
	SourceSchemaRegistryURL string     `json:"source_schema_registry_url"`
	Exporters               []Exporter `json:"exporters"`
}

type Exporter struct {
	Name        string   `json:"name"`
	ContextType string   `json:"context_type"`
	ContextName string   `json:"context_name"`
	Subjects    []string `json:"subjects"`
}
