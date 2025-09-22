package types

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	kafkaconnecttypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/confluentinc/kcp/internal/build_info"
)

type State struct {
	Regions      []DiscoveredRegion `json:"regions"`
	KcpBuildInfo KcpBuildInfo       `json:"kcp_build_info"`
	Timestamp    time.Time          `json:"timestamp"`
}

func NewState(discoveredRegions []DiscoveredRegion) State {
	return State{
		Regions: discoveredRegions,
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}
}

func (s *State) WriteToJsonFile(filePath string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %v", err)
	}
	return os.WriteFile(filePath, data, 0644)
}

func (s *State) LoadStateFile(stateFile string) error {
	file, err := os.ReadFile(stateFile)
	if err != nil {
		return fmt.Errorf("failed to read state file: %v", err)
	}

	if err := json.Unmarshal(file, s); err != nil {
		return fmt.Errorf("failed to unmarshal discovery: %v", err)
	}

	return nil
}

func (s *State) PersistStateFile(stateFile string) error {
	if s == nil {
		return fmt.Errorf("discovery state is nil")
	}

	return s.WriteToJsonFile(stateFile)
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
	Region                      string                      `json:"region"`
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
	Connectors           []ConnectorSummary                     `json:"connectors"`
}

// Returns only one bootstrap broker per authentication type.
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

// Returns all bootstrap brokers for a given auth type.
func (c *AWSClientInformation) GetAllBootstrapBrokersForAuthType(authType AuthType) ([]string, error) {
	var brokerList []string
	slog.Info("🔍 parsing broker addresses", "authType", authType)

	switch authType {
	case AuthTypeIAM:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslIam))
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslIam))
	case AuthTypeSASLSCRAM:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicSaslScram))
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringSaslScram))
	case AuthTypeUnauthenticated:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls))
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerString))
	case AuthTypeTLS:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringPublicTls))
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls))
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", authType)
	}

	slog.Info("🔍 found broker addresses", "authType", authType, "addresses", brokerList)

	rawAddresses := strings.Split(strings.Join(brokerList, ","), ",")
	addresses := make([]string, 0, len(rawAddresses))
	for _, addr := range rawAddresses {
		trimmedAddr := strings.TrimSpace(addr)
		if trimmedAddr != "" {
			addresses = append(addresses, trimmedAddr)
		}
	}
	return addresses, nil
}

type ConnectorSummary struct {
	ConnectorArn                     string                                                        `json:"connector_arn"`
	ConnectorName                    string                                                        `json:"connector_name"`
	ConnectorState                   string                                                        `json:"connector_state"`
	CreationTime                     string                                                        `json:"creation_time"`
	KafkaCluster                     kafkaconnecttypes.ApacheKafkaClusterDescription               `json:"kafka_cluster"`
	KafkaClusterClientAuthentication kafkaconnecttypes.KafkaClusterClientAuthenticationDescription `json:"kafka_cluster_client_authentication"`
	Capacity                         kafkaconnecttypes.CapacityDescription                         `json:"capacity"`
	Plugins                          []kafkaconnecttypes.PluginDescription                         `json:"plugins"`
	ConnectorConfiguration           map[string]string                                             `json:"connector_configuration"`
}

type KafkaAdminClientInformation struct {
	ClusterID string  `json:"cluster_id"`
	Topics    *Topics `json:"topics"`
	Acls      []Acls  `json:"acls"`
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

type KcpBuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}
