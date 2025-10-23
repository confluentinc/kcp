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
	NeedsEnvironment bool   `json:"needs_environment"`
	EnvironmentName  string `json:"environment_name"`
	EnvironmentId    string `json:"environment_id"`
	NeedsCluster     bool   `json:"needs_cluster"`
	ClusterName      string `json:"cluster_name"`
	ClusterType      string `json:"cluster_type"`
}

type TerraformFiles struct {
	MainTf      string `json:"main_tf"`
	ProvidersTf string `json:"providers_tf"`
	VariablesTf string `json:"variables_tf"`
}

type MigrationWizardRequest struct {
	MskPubliclyAccessible        bool   `json:"msk_publicly_accessible"`
	AuthenticationMethod         string `json:"authentication_method"`
	TargetClusterType            string `json:"target_cluster_type"`
	TargetEnvironmentId          string `json:"target_environment_id"`
	TargetClusterId              string `json:"target_cluster_id"`
	TargetRestEndpoint           string `json:"target_rest_endpoint"`
	MskClusterId                 string `json:"msk_cluster_id"`
	MskSaslScramBootstrapServers string `json:"msk_sasl_scram_bootstrap_servers"`
}
