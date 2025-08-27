package types

import (
	"fmt"
	"strconv"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// DefaultClientBrokerEncryptionInTransit is the fallback encryption type when cluster encryption info is not available
const DefaultClientBrokerEncryptionInTransit = kafkatypes.ClientBrokerTls

// GetClientBrokerEncryptionInTransit determines the client broker encryption in transit value for a cluster
// with proper fallback logic when encryption info is not available
func GetClientBrokerEncryptionInTransit(cluster kafkatypes.Cluster) kafkatypes.ClientBroker {
	if cluster.ClusterType == kafkatypes.ClusterTypeProvisioned &&
		cluster.Provisioned != nil &&
		cluster.Provisioned.EncryptionInfo != nil &&
		cluster.Provisioned.EncryptionInfo.EncryptionInTransit != nil {
		return cluster.Provisioned.EncryptionInfo.EncryptionInTransit.ClientBroker
	}
	return DefaultClientBrokerEncryptionInTransit
}

// Preferred over sarama.ResourceAcls because it is flattened vs sarama's nested structure.
type Acls struct {
	ResourceType        string `json:"ResourceType"`
	ResourceName        string `json:"ResourceName"`
	ResourcePatternType string `json:"ResourcePatternType"`
	Principal           string `json:"Principal"`
	Host                string `json:"Host"`
	Operation           string `json:"Operation"`
	PermissionType      string `json:"PermissionType"`
}

// ClusterSummary contains summary information about an MSK cluster
type ClusterSummary struct {
	ClusterName                     string                  `json:"cluster_name"`
	ClusterARN                      string                  `json:"cluster_arn"`
	Status                          string                  `json:"status"`
	Type                            string                  `json:"type"`
	Authentication                  string                  `json:"authentication"`
	PublicAccess                    bool                    `json:"public_access"`
	ClientBrokerEncryptionInTransit kafkatypes.ClientBroker `json:"client_broker_encryption_in_transit"`
}

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
	AuthTypeSASLSCRAM       AuthType = "SASL/SCRAM"
	AuthTypeIAM             AuthType = "SASL/IAM"
	AuthTypeTLS             AuthType = "TLS"
	AuthTypeUnauthenticated AuthType = "Unauthenticated"
)

func (a AuthType) IsValid() bool {
	switch a {
	case AuthTypeSASLSCRAM, AuthTypeIAM, AuthTypeTLS, AuthTypeUnauthenticated:
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
		string(AuthTypeUnauthenticated),
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

type ACLMapping struct {
	Operation       string
	ResourceType    string
	RequiresPattern bool
}

// https://docs.aws.amazon.com/service-authorization/latest/reference/list_apachekafkaapisforamazonmskclusters.html
// https://docs.confluent.io/cloud/current/security/access-control/acl.html#acl-resources-and-operations-for-ccloud-summary
var AclMap = map[string]ACLMapping{
	"kafka-cluster:AlterCluster": {
		Operation:       "Alter",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:AlterClusterDynamicConfiguration": {
		Operation:       "AlterConfigs",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:AlterGroup": {
		Operation:       "Read",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTopic": {
		Operation:       "Alter",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTopicDynamicConfiguration": {
		Operation:       "AlterConfigs",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:AlterTransactionalId": {
		Operation:       "Write",
		ResourceType:    "TransactionalId",
		RequiresPattern: true,
	},
	"kafka-cluster:CreateTopic": {
		Operation:       "Create",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DeleteGroup": {
		Operation:       "Delete",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:DeleteTopic": {
		Operation:       "Delete",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeCluster": {
		Operation:       "Describe",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:DescribeClusterDynamicConfiguration": {
		Operation:       "DescribeConfigs",
		ResourceType:    "Cluster",
		RequiresPattern: false,
	},
	"kafka-cluster:DescribeGroup": {
		Operation:       "Describe",
		ResourceType:    "Group",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTopic": {
		Operation:       "Describe",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTopicDynamicConfiguration": {
		Operation:       "DescribeConfigs",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTransactionalId": {
		Operation:       "Describe",
		ResourceType:    "TransactionalId",
		RequiresPattern: true,
	},
	"kafka-cluster:ReadData": {
		Operation:       "Read",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:WriteData": {
		Operation:       "Write",
		ResourceType:    "Topic",
		RequiresPattern: true,
	},
	"kafka-cluster:WriteDataIdempotently": {
		Operation:       "IdempotentWrite",
		ResourceType:    "Cluster",
		RequiresPattern: true,
	},
}

// YAML structures for creds.yaml
type CredsYaml map[string]RegionEntries

type RegionEntries map[string]ClusterEntry

type ClusterEntry struct {
	UseSASLIAM         *bool            `yaml:"use_sasl_iam,omitempty"`
	UseSASLScram       *bool            `yaml:"use_sasl_scram,omitempty"`
	UseTLS             *bool            `yaml:"use_tls,omitempty"`
	UseUnauthenticated *bool            `yaml:"use_unauthenticated,omitempty"`
	SkipKafka          *bool            `yaml:"skip_kafka,omitempty"`
	SASLScram          *SASLScramConfig `yaml:"sasl_scram,omitempty"`
	TLS                *TLSConfig       `yaml:"tls,omitempty"`
}

type SASLScramConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type TLSConfig struct {
	CACert     string `yaml:"ca_cert"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
}
