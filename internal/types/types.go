package types

import (
	"fmt"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
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

type ClusterInformation struct {
	ClusterID            string                                 `json:"cluster_id"`
	Region               string                                 `json:"region"`
	Timestamp            time.Time                              `json:"timestamp"`
	Cluster              kafkatypes.Cluster                     `json:"cluster"`
	ClientVpcConnections []kafkatypes.ClientVpcConnection       `json:"clientVpcConnections"`
	ClusterOperations    []kafkatypes.ClusterOperationV2Summary `json:"clusterOperations"`
	Nodes                []kafkatypes.NodeInfo                  `json:"nodes"`
	ScramSecrets         []string                               `json:"ScramSecrets"`
	BootstrapBrokers     kafka.GetBootstrapBrokersOutput        `json:"bootstrapBrokers"`
	Policy               kafka.GetClusterPolicyOutput           `json:"policy"`
	CompatibleVersions   kafka.GetCompatibleKafkaVersionsOutput `json:"compatibleVersions"`
	Topics               []string                               `json:"topics"`
	Acls                 []sarama.ResourceAcls                  `json:"acls"`
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

type RegionScanResult struct {
	Timestamp      time.Time                                   `json:"timestamp"`
	Clusters       []ClusterSummary                            `json:"clusters"`
	VpcConnections []kafkatypes.VpcConnection                  `json:"vpc_connections"`
	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
	KafkaVersions  []kafkatypes.KafkaVersion                   `json:"kafka_versions"`
	Replicators    []kafka.DescribeReplicatorOutput            `json:"replicators"`
	Region         string                                      `json:"region"`
}

// MSKClusterMetrics represents detailed metrics for an MSK cluster
type MSKClusterMetrics struct {
	Region         string           `json:"region"`
	ClusterMetrics []ClusterMetrics `json:"cluster_metrics"`
	CostData       CostData         `json:"cost_data"`
}

type RegionMetrics struct {
	Region         string           `json:"region"`
	ClusterMetrics []ClusterMetrics `json:"cluster_metrics"`
}

type RegionCosts struct {
	Region   string   `json:"region"`
	CostData CostData `json:"cost_data"`
}

type ClusterMetricsSummary struct {
	AvgIngressThroughputMegabytesPerSecond  *float64 `json:"avg_ingress_throughput_megabytes_per_second"`
	PeakIngressThroughputMegabytesPerSecond *float64 `json:"peak_ingress_throughput_megabytes_per_second"`
	AvgEgressThroughputMegabytesPerSecond   *float64 `json:"avg_egress_throughput_megabytes_per_second"`
	PeakEgressThroughputMegabytesPerSecond  *float64 `json:"peak_egress_throughput_megabytes_per_second"`
	// 	Retention (Days)
	RetentionDays *float64 `json:"retention_days,omitempty"`
	// Partitions (Optional, Default = 1000)
	Partitions *float64 `json:"partitions"`
	// Replication Factor (Optional, Default = 3)
	ReplicationFactor *float64 `json:"replication_factor,omitempty"`
	// Follower Fetching (Default = FALSE)
	FollowerFetching *bool `json:"follower_fetching"`
	// Tiered Storage (Default = FALSE)
	TieredStorage *bool `json:"tiered_storage"`
	// "Local Retention in Primary Storage (Hrs)
	// ** leave blank if TS = FALSE"
	LocalRetentionInPrimaryStorageHours *float64 `json:"local_retention_in_primary_storage_hours,omitempty"`
	// "Instance Type Override
	// Otherwise Defaults used based on peak ingress:
	// <100 MB/s=M7g.l,>=100 and <300=M7g.xl, >=300 and <600=M7g.2xl, >=600=M7g.4xl"
	InstanceType *string `json:"instance_type"`
}

type ClusterMetrics struct {
	ClusterName           string                `json:"cluster_name"`
	ClusterType           string                `json:"cluster_type"`
	BrokerAZDistribution  *string               `json:"broker_az_distribution"`
	Authentication        map[string]any        `json:"authentication"`
	KafkaVersion          *string               `json:"kafka_version"`
	EnhancedMonitoring    *string               `json:"enhanced_monitoring"`
	NodesMetrics          []NodeMetrics         `json:"nodes"`
	ClusterMetricsSummary ClusterMetricsSummary `json:"cluster_metrics_summary"`
}

type NodeMetrics struct {
	NodeID                       int     `json:"node_id"`
	InstanceType                 *string `json:"instance_type"`
	VolumeSizeGB                 int     `json:"volume_size_gb"`
	BytesInPerSecAvg             float64 `json:"bytes_in_per_sec_avg"`
	BytesOutPerSecAvg            float64 `json:"bytes_out_per_sec_avg"`
	MessagesInPerSecAvg          float64 `json:"messages_in_per_sec_avg"`
	KafkaDataLogsDiskUsedAvg     float64 `json:"kafka_data_logs_disk_used_avg"`
	BytesInPerSecMax             float64 `json:"bytes_in_per_sec_max"`
	BytesOutPerSecMax            float64 `json:"bytes_out_per_sec_max"`
	MessagesInPerSecMax          float64 `json:"messages_in_per_sec_max"`
	KafkaDataLogsDiskUsedMax     float64 `json:"kafka_data_logs_disk_used_max"`
	ClientConnectionCountMax     float64 `json:"client_connection_count_max"`
	PartitionCountMax            float64 `json:"partition_count_max"`
	GlobalTopicCountMax          float64 `json:"global_topic_count_max"`
	LeaderCountMax               float64 `json:"leader_count_max"`
	ReplicationBytesOutPerSecMax float64 `json:"replication_bytes_out_per_sec_max"`
	ReplicationBytesInPerSecMax  float64 `json:"replication_bytes_in_per_sec_max"`
}

type CostData struct {
	Costs []Cost  `json:"costs"`
	Total float64 `json:"total"`
}

type Cost struct {
	TimePeriodStart string  `json:"time_period_start"`
	TimePeriodEnd   string  `json:"time_period_end"`
	Service         string  `json:"service"`
	Cost            float64 `json:"cost"`
	UsageType       string  `json:"usage_type"`
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
