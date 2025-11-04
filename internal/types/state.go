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
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/kcp/internal/build_info"
)

// State represents the raw input data structure (kcp-state.json file)
// This is what gets fed INTO the frontend/API for processing
type State struct {
	Regions          []DiscoveredRegion          `json:"regions"`
	SchemaRegistries []SchemaRegistryInformation `json:"schema_registries"`
	KcpBuildInfo     KcpBuildInfo                `json:"kcp_build_info"`
	Timestamp        time.Time                   `json:"timestamp"`
}

func NewStateFrom(fromState *State) *State {
	// Always create with fresh metadata for the current discovery run
	workingState := &State{
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}

	if fromState == nil {
		workingState.Regions = []DiscoveredRegion{}
	} else {
		// Copy existing regions to preserve untouched regions
		workingState.Regions = make([]DiscoveredRegion, len(fromState.Regions))
		copy(workingState.Regions, fromState.Regions)
	}

	return workingState
}

func NewStateFromFile(stateFile string) (*State, error) {
	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %v", err)
	}

	var state State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %v", err)
	}

	return &state, nil
}

func (s *State) WriteToFile(filePath string) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %v", err)
	}
	return os.WriteFile(filePath, data, 0644)
}

func (s *State) PersistStateFile(stateFile string) error {
	if s == nil {
		return fmt.Errorf("discovery state is nil")
	}

	return s.WriteToFile(stateFile)
}

// UpsertRegion inserts a new region or updates an existing one by name
// Automatically preserves KafkaAdminClientInformation from existing clusters
func (s *State) UpsertRegion(newRegion DiscoveredRegion) {
	for i, existingRegion := range s.Regions {
		if existingRegion.Name == newRegion.Name {
			discoveredClusters := newRegion.Clusters
			newRegion.Clusters = existingRegion.Clusters
			// set discovered clusters and refresh into state (preserves KafkaAdminClientInformation)
			newRegion.RefreshClusters(discoveredClusters)
			s.Regions[i] = newRegion
			return
		}
	}
	s.Regions = append(s.Regions, newRegion)
}

type DiscoveredRegion struct {
	Name           string                                      `json:"name"`
	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
	Costs          CostInformation                             `json:"costs"`
	Clusters       []DiscoveredCluster                         `json:"clusters"`
	// internal only - exclude from JSON output
	ClusterArns []string `json:"-"`
}

// RefreshClusters replaces the cluster list but preserves KafkaAdminClientInformation from existing clusters
func (dr *DiscoveredRegion) RefreshClusters(newClusters []DiscoveredCluster) {
	// build map of ARN -> KafkaAdminClientInformation from existing clusters
	adminInfoByArn := make(map[string]KafkaAdminClientInformation)
	for _, existingCluster := range dr.Clusters {
		adminInfoByArn[existingCluster.Arn] = existingCluster.KafkaAdminClientInformation
	}

	// replace cluster list with new discoveries
	dr.Clusters = newClusters

	// restore admin info for clusters that still exist
	for i := range dr.Clusters {
		if adminInfo, exists := adminInfoByArn[dr.Clusters[i].Arn]; exists {
			dr.Clusters[i].KafkaAdminClientInformation = adminInfo
		}
	}
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
	case AuthTypeUnauthenticatedTLS:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls)
		visibility = "PRIVATE"
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No Unauthenticated (TLS Encryption) brokers found in the cluster")
		}
	case AuthTypeUnauthenticatedPlaintext:
		brokerList = aws.ToString(c.BootstrapBrokers.BootstrapBrokerString)
		visibility = "PRIVATE"
		if brokerList == "" {
			return nil, fmt.Errorf("❌ No Unauthenticated (Plaintext) brokers found in the cluster")
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
	case AuthTypeUnauthenticatedTLS:
		brokerList = append(brokerList, aws.ToString(c.BootstrapBrokers.BootstrapBrokerStringTls))
	case AuthTypeUnauthenticatedPlaintext:
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

type ClusterNetworking struct {
	VpcId          string       `json:"vpc_id"`
	SubnetIds      []string     `json:"subnet_ids"`
	SecurityGroups []string     `json:"security_groups"`
	Subnets        []SubnetInfo `json:"subnets"`
}

type SubnetInfo struct {
	SubnetMskBrokerId int    `json:"subnet_msk_broker_id"`
	SubnetId          string `json:"subnet_id"`
	AvailabilityZone  string `json:"availability_zone"`
	PrivateIpAddress  string `json:"private_ip_address"`
	CidrBlock         string `json:"cidr_block"`
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

type SelfManagedConnector struct {
	Name        string         `json:"name"`
	Config      map[string]any `json:"config"`
	State       string         `json:"state,omitempty"`
	ConnectHost string         `json:"connect_host,omitempty"`
}

type SelfManagedConnectors struct {
	Connectors []SelfManagedConnector `json:"connectors"`
}

type KafkaAdminClientInformation struct {
	ClusterID             string                 `json:"cluster_id"`
	Topics                *Topics                `json:"topics"`
	Acls                  []Acls                 `json:"acls"`
	SelfManagedConnectors *SelfManagedConnectors `json:"self_managed_connectors"`
}

func (c *KafkaAdminClientInformation) CalculateTopicSummary() TopicSummary {
	return CalculateTopicSummaryFromDetails(c.Topics.Details)
}

func (c *KafkaAdminClientInformation) SetTopics(topicDetails []TopicDetails) {
	c.Topics = &Topics{
		Details: topicDetails,
		Summary: CalculateTopicSummaryFromDetails(topicDetails),
	}
}

func (c *KafkaAdminClientInformation) SetSelfManagedConnectors(connectors []SelfManagedConnector) {
	c.SelfManagedConnectors = &SelfManagedConnectors{
		Connectors: connectors,
	}
}

// ----- metrics -----
type ClusterMetrics struct {
	MetricMetadata MetricMetadata                     `json:"metadata"`
	Results        []cloudwatchtypes.MetricDataResult `json:"results"`
}

type MetricMetadata struct {
	ClusterType          string    `json:"cluster_type"`
	KafkaVersion         string    `json:"kafka_version"`
	BrokerAzDistribution string    `json:"broker_az_distribution"`
	EnhancedMonitoring   string    `json:"enhanced_monitoring"`
	StartDate            time.Time `json:"start_date"`
	EndDate              time.Time `json:"end_date"`
	Period               int32     `json:"period"`

	FollowerFetching bool   `json:"follower_fetching"`
	InstanceType     string `json:"instance_type"`
	TieredStorage    bool   `json:"tiered_storage"`
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

type SchemaRegistryInformation struct {
	Type                 string                       `json:"type"`
	URL                  string                       `json:"url"`
	DefaultCompatibility schemaregistry.Compatibility `json:"default_compatibility"`
	Contexts             []string                     `json:"contexts"`
	Subjects             []Subject                    `json:"subjects"`
}

type Subject struct {
	Name          string                          `json:"name"`
	SchemaType    string                          `json:"schema_type"`
	Compatibility string                          `json:"compatibility,omitempty"`
	Versions      []schemaregistry.SchemaMetadata `json:"versions"`
	Latest        schemaregistry.SchemaMetadata   `json:"latest_schema"`
}

// ProcessedState represents the transformed output data structure
// This is what comes OUT of the frontend/API after processing the raw State data
// Same structure as State but with costs and metrics flattened for easier frontend consumption
type ProcessedState struct {
	Regions          []ProcessedRegion           `json:"regions"`
	SchemaRegistries []SchemaRegistryInformation `json:"schema_registries"`
	KcpBuildInfo     KcpBuildInfo                `json:"kcp_build_info"`
	Timestamp        time.Time                   `json:"timestamp"`
}

// ProcessedRegion mirrors DiscoveredRegion but with flattened costs and simplified clusters
type ProcessedRegion struct {
	Name           string                                      `json:"name"`
	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
	Costs          ProcessedRegionCosts                        `json:"costs"`    // Flattened from raw AWS Cost Explorer data
	Clusters       []ProcessedCluster                          `json:"clusters"` // Simplified from full DiscoveredCluster data
}

type ProcessedRegionCosts struct {
	Region     string              `json:"region"`
	Metadata   CostMetadata        `json:"metadata"`
	Results    []ProcessedCost     `json:"results"`
	Aggregates ProcessedAggregates `json:"aggregates"`
}

// ProcessedAggregates represents the three specific services we query
type ProcessedAggregates struct {
	AWSCertificateManager                ServiceCostAggregates `json:"AWS Certificate Manager"`
	AmazonManagedStreamingForApacheKafka ServiceCostAggregates `json:"Amazon Managed Streaming for Apache Kafka"`
	EC2Other                             ServiceCostAggregates `json:"EC2 - Other"`
}

// NewProcessedAggregates creates a new ProcessedAggregates with all maps initialized
func NewProcessedAggregates() ProcessedAggregates {
	return ProcessedAggregates{
		AWSCertificateManager: ServiceCostAggregates{
			UnblendedCost:    make(map[string]any),
			BlendedCost:      make(map[string]any),
			AmortizedCost:    make(map[string]any),
			NetAmortizedCost: make(map[string]any),
			NetUnblendedCost: make(map[string]any),
		},
		AmazonManagedStreamingForApacheKafka: ServiceCostAggregates{
			UnblendedCost:    make(map[string]any),
			BlendedCost:      make(map[string]any),
			AmortizedCost:    make(map[string]any),
			NetAmortizedCost: make(map[string]any),
			NetUnblendedCost: make(map[string]any),
		},
		EC2Other: ServiceCostAggregates{
			UnblendedCost:    make(map[string]any),
			BlendedCost:      make(map[string]any),
			AmortizedCost:    make(map[string]any),
			NetAmortizedCost: make(map[string]any),
			NetUnblendedCost: make(map[string]any),
		},
	}
}

type ProcessedCost struct {
	Start     string                 `json:"start"`
	End       string                 `json:"end"`
	Service   string                 `json:"service"`
	UsageType string                 `json:"usage_type"`
	Values    ProcessedCostBreakdown `json:"values"`
}

type ProcessedCostBreakdown struct {
	UnblendedCost    float64 `json:"unblended_cost"`
	BlendedCost      float64 `json:"blended_cost"`
	AmortizedCost    float64 `json:"amortized_cost"`
	NetAmortizedCost float64 `json:"net_amortized_cost"`
	NetUnblendedCost float64 `json:"net_unblended_cost"`
}

// ProcessedCluster contains the complete cluster data with flattened metrics
// This is the full cluster information with processed metrics, unlike the simplified version in types.go
type ProcessedCluster struct {
	Name                        string                      `json:"name"`
	Arn                         string                      `json:"arn"`
	Region                      string                      `json:"region"`
	ClusterMetrics              ProcessedClusterMetrics     `json:"metrics"` // Flattened from raw CloudWatch metrics
	AWSClientInformation        AWSClientInformation        `json:"aws_client_information"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
}

type ProcessedClusterMetrics struct {
	Region     string                     `json:"region"`
	ClusterArn string                     `json:"cluster_arn"`
	Metadata   MetricMetadata             `json:"metadata"`
	Metrics    []ProcessedMetric          `json:"results"`
	Aggregates map[string]MetricAggregate `json:"aggregates"`
}

type ProcessedMetric struct {
	Start string   `json:"start"`
	End   string   `json:"end"`
	Label string   `json:"label"`
	Value *float64 `json:"value"`
}

type MetricAggregate struct {
	Average *float64 `json:"avg"`
	Maximum *float64 `json:"max"`
	Minimum *float64 `json:"min"`
}

type CostAggregate struct {
	Sum     *float64 `json:"sum"`
	Average *float64 `json:"avg"`
	Maximum *float64 `json:"max"`
	Minimum *float64 `json:"min"`
}

// ServiceCostAggregates represents cost aggregates for a single service
// Uses explicit fields for each metric type instead of a map
type ServiceCostAggregates struct {
	UnblendedCost    map[string]any `json:"unblended_cost"`
	BlendedCost      map[string]any `json:"blended_cost"`
	AmortizedCost    map[string]any `json:"amortized_cost"`
	NetAmortizedCost map[string]any `json:"net_amortized_cost"`
	NetUnblendedCost map[string]any `json:"net_unblended_cost"`
}
