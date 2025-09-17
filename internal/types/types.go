package types

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/build_info"
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

type KcpBuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type Discovery struct {
	Regions      []DiscoveredRegion `json:"regions"`
	KcpBuildInfo KcpBuildInfo       `json:"kcp_build_info"`
	Timestamp    time.Time          `json:"timestamp"`
}

func NewDiscovery(discoveredRegions []DiscoveredRegion) Discovery {
	return Discovery{
		Regions: discoveredRegions,
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}
}

func (d *Discovery) WriteToJsonFile(filePath string) error {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal discovery: %v", err)
	}
	return os.WriteFile(filePath, data, 0644)
}

func (d *Discovery) LoadStateFile(stateFile string) error {
	file, err := os.ReadFile(stateFile)
	if err != nil {
		return fmt.Errorf("failed to read state file: %v", err)
	}

	if err := json.Unmarshal(file, d); err != nil {
		return fmt.Errorf("failed to unmarshal discovery: %v", err)
	}

	return nil
}

func (d *Discovery) PersistStateFile(stateFile string) error {
	if d == nil {
		return fmt.Errorf("discovery state is nil")
	}

	return d.WriteToJsonFile(stateFile)
}

type DiscoveredRegion struct {
	Name           string                                      `json:"name"`
	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
	Costs          CostInformation                             `json:"costs"`
	Clusters       []DiscoveredCluster                         `json:"clusters"`
	// internal only - exclude from JSON output
	ClusterArns []string `json:"-"`
}

type DiscoveredCluster struct {
	Name                        string                      `json:"name"`
	Arn                         string                      `json:"arn"`
	ClusterMetrics              ClusterMetrics              `json:"metrics"`
	AWSClientInformation        AWSClientInformation        `json:"aws_client_information"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
}

type AWSClientInformation struct {
	MskClusterConfig     kafkatypes.Cluster                     `json:"msk_cluster_config"`
	ClientVpcConnections []kafkatypes.ClientVpcConnection       `json:"client_vpc_connections"`
	ClusterOperations    []kafkatypes.ClusterOperationV2Summary `json:"cluster_operations"`
	Nodes                []kafkatypes.NodeInfo                  `json:"nodes"`
	ScramSecrets         []string                               `json:"ScramSecrets"`
	BootstrapBrokers     kafka.GetBootstrapBrokersOutput        `json:"bootstrap_brokers"`
	Policy               kafka.GetClusterPolicyOutput           `json:"policy"`
	CompatibleVersions   kafka.GetCompatibleKafkaVersionsOutput `json:"compatible_versions"`
	ClusterNetworking    ClusterNetworking                      `json:"cluster_networking"`
}

type KafkaAdminClientInformation struct {
	ClusterID string  `json:"cluster_id"`
	Topics    *Topics `json:"topics"`
	Acls      []Acls  `json:"acls"`
}

// ----- metrics -----
type ClusterMetrics struct {
	MetricMetadata MetricMetadata                     `json:"metadata"`
	Results        []cloudwatchtypes.MetricDataResult `json:"results"`
}

type MetricMetadata struct {
	ClusterType          string `json:"cluster_type"`
	FollowerFetching     bool   `json:"follower_fetching"`
	BrokerAzDistribution string `json:"broker_az_distribution"`
	KafkaVersion         string `json:"kafka_version"`
	EnhancedMonitoring   string `json:"enhanced_monitoring"`
	StartWindowDate      string `json:"start_window_date"`
	EndWindowDate        string `json:"end_window_date"`
	Period               int32  `json:"period"`
}

type CloudWatchTimeWindow struct {
	StartTime time.Time
	EndTime   time.Time
	Period    int32
}

// ----- costs -----
type CostInformation struct {
	CostMetadata CostMetadata                     `json:"metadata"`
	CostResults  []costexplorertypes.ResultByTime `json:"results"`
}

type CostMetadata struct {
	StartDate   time.Time           `json:"start_date"`
	EndDate     time.Time           `json:"end_date"`
	Granularity string              `json:"granularity"`
	Tags        map[string][]string `json:"tags"`
	Services    []string            `json:"services"`
}

// / todo review if we need
type GlobalMetrics struct {
	GlobalPartitionCountMax float64 `json:"global_partition_count_max"`
	GlobalTopicCountMax     float64 `json:"global_topic_count_max"`
}

func (c *AWSClientInformation) GetBootstrapBrokersForAuthType(authType AuthType) ([]string, error) {
	var brokerList string
	var visibility string
	slog.Info("🔍 parsing broker addresses", "authType", authType)

	switch authType {
	case AuthTypeIAM:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslIam)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslIam)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No SASL/IAM brokers found in the cluster")
		}
	case AuthTypeSASLSCRAM:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslScram)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No SASL/SCRAM brokers found in the cluster")
		}
	case AuthTypeUnauthenticated:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
		visibility = "PRIVATE"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerString)
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No Unauthenticated brokers found in the cluster")
		}
	case AuthTypeTLS:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicTls)
		visibility = "PUBLIC"
		if brokerList == "" {
			brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
			visibility = "PRIVATE"
		}
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No TLS brokers found in the cluster")
		}
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", authType)
	}

	slog.Info("🔍 found broker addresses", "visibility", visibility, "authType", authType, "addresses", brokerList)

	// Split by comma and trim whitespace from each address, filter out empty strings
	rawAddresses := strings.Split(brokerList, ",")
	addresses := make([]string, 0, len(rawAddresses))
	for _, addr := range rawAddresses {
		trimmedAddr := strings.TrimSpace(addr)
		if trimmedAddr != "" {
			addresses = append(addresses, trimmedAddr)
		}
	}
	return addresses, nil
}

func (c *KafkaAdminClientInformation) CalculateTopicSummary() TopicSummary {
	return c.CalculateTopicSummaryFromDetails(c.Topics.Details)
}

func (c *KafkaAdminClientInformation) CalculateTopicSummaryFromDetails(topicDetails []TopicDetails) TopicSummary {
	summary := TopicSummary{}

	for _, topic := range topicDetails {
		isInternal := strings.HasPrefix(topic.Name, "__")

		// Check if cleanup.policy exists and is not nil before dereferencing
		var isCompact bool
		if cleanupPolicy, exists := topic.Configurations["cleanup.policy"]; exists && cleanupPolicy != nil {
			isCompact = strings.Contains(*cleanupPolicy, "compact")
		}

		if isInternal {
			summary.InternalTopics++
			summary.TotalInternalPartitions += topic.Partitions
			if isCompact {
				summary.CompactInternalTopics++
				summary.CompactInternalPartitions += topic.Partitions
			}
		} else {
			summary.Topics++
			summary.TotalPartitions += topic.Partitions
			if isCompact {
				summary.CompactTopics++
				summary.CompactPartitions += topic.Partitions
			}
		}
	}

	return summary
}

func (c *KafkaAdminClientInformation) SetTopics(topicDetails []TopicDetails) {
	c.Topics = &Topics{
		Details: topicDetails,
		Summary: CalculateTopicSummaryFromDetails(topicDetails),
	}
}
