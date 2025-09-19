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
	"github.com/confluentinc/kcp/internal/services/markdown"
)

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

// report type

// ParsedCost represents a flattened cost entry
type ProcessedCost struct {
	Start     string `json:"start"`
	End       string `json:"end"`
	Service   string `json:"service"`
	UsageType string `json:"usage_type"`
	Value     string `json:"value"`
}

// ServiceTotal represents the total cost for a service
type ServiceTotal struct {
	Service string `json:"service"`
	Total   string `json:"total"`
}

// ParsedCostResponse represents the response structure with parsed costs and totals
type ProcessedRegionCosts struct {
	Region   string          `json:"region"`
	Costs    []ProcessedCost `json:"costs"`
	Totals   []ServiceTotal  `json:"totals"`
	Metadata CostMetadata    `json:"metadata"`
}

// todo this could be use for a single report with both costs and metrics - with its own markdown func
type Report struct {
	Costs   []ProcessedRegionCosts    `json:"costs"`
	Metrics []ProcessedClusterMetrics `json:"metrics"`
}

type CostReport struct {
	ProcessedRegionCosts []ProcessedRegionCosts `json:"processed_region_costs"`
}

func (c *CostReport) AsMarkdown() *markdown.Markdown {
	md := markdown.New()
	md.AddHeading("AWS Service Cost Report", 1)

	md.AddParagraph("This report presents cost analysis for AWS services across multiple regions.")

	// Process each region
	for _, regionCost := range c.ProcessedRegionCosts {
		md.AddHeading(fmt.Sprintf("Region: %s", regionCost.Region), 2)
		md.AddParagraph(fmt.Sprintf("This section presents cost analysis for the region **%s**.", regionCost.Region))

		// Add metadata section
		md.AddHeading("Cost Analysis Dimensions", 3)
		services := make([]string, 0, len(regionCost.Totals))
		for _, total := range regionCost.Totals {
			services = append(services, total.Service)
		}

		md.AddParagraph(fmt.Sprintf("**Region:** %s", regionCost.Region))
		md.AddParagraph(fmt.Sprintf("**Services:** %s", strings.Join(services, ", ")))
		md.AddParagraph(fmt.Sprintf("**Aggregation Period:** %s to %s", regionCost.Metadata.StartDate.Format("2006-01-02"), regionCost.Metadata.EndDate.Format("2006-01-02")))
		md.AddParagraph(fmt.Sprintf("**Aggregation Granularity:** %s", regionCost.Metadata.Granularity))

		if len(regionCost.Metadata.Tags) > 0 {
			md.AddParagraph("**Resource Filter Tags:**")
			for k, v := range regionCost.Metadata.Tags {
				md.AddParagraph(fmt.Sprintf("- %s=%s", k, strings.Join(v, ",")))
			}
		}

		// Build usage type summary
		usageTypeSummary := make(map[string]map[string]float64) // service -> lineItem -> cost
		serviceTotalsFromCosts := make(map[string]float64)      // service -> total cost

		for _, cost := range regionCost.Costs {
			if usageTypeSummary[cost.Service] == nil {
				usageTypeSummary[cost.Service] = make(map[string]float64)
			}

			// Parse cost string to float
			if costFloat, err := strconv.ParseFloat(cost.Value, 64); err == nil {
				usageTypeSummary[cost.Service][cost.UsageType] += costFloat
				serviceTotalsFromCosts[cost.Service] += costFloat
			}
		}

		// Separate MSK and other services
		mskUsageData := [][]string{}
		otherUsageData := [][]string{}
		var mskDataTransferCost, ec2DataTransferCost float64

		// Sort services for consistent output
		sortedServices := make([]string, 0, len(usageTypeSummary))
		for service := range usageTypeSummary {
			sortedServices = append(sortedServices, service)
		}
		// Simple sort
		for i := 0; i < len(sortedServices)-1; i++ {
			for j := i + 1; j < len(sortedServices); j++ {
				if sortedServices[i] > sortedServices[j] {
					sortedServices[i], sortedServices[j] = sortedServices[j], sortedServices[i]
				}
			}
		}

		for _, service := range sortedServices {
			// Sort line items within each service
			lineItems := make([]string, 0, len(usageTypeSummary[service]))
			for lineItem := range usageTypeSummary[service] {
				lineItems = append(lineItems, lineItem)
			}
			for i := 0; i < len(lineItems)-1; i++ {
				for j := i + 1; j < len(lineItems); j++ {
					if lineItems[i] > lineItems[j] {
						lineItems[i], lineItems[j] = lineItems[j], lineItems[i]
					}
				}
			}

			for _, lineItem := range lineItems {
				totalCost := usageTypeSummary[service][lineItem]
				totalCostFormatted := fmt.Sprintf("$%.2f", totalCost)
				if totalCostFormatted == "$0.00" {
					continue
				}

				if service == "Amazon Managed Streaming for Apache Kafka" {
					if strings.Contains(lineItem, "DataTransfer-Regional-Bytes") {
						mskDataTransferCost = totalCost
					}
					mskUsageData = append(mskUsageData, []string{lineItem, totalCostFormatted})
				} else {
					if strings.Contains(lineItem, "DataTransfer-Regional-Bytes") && service == "EC2 - Other" {
						ec2DataTransferCost = totalCost
					}
					otherUsageData = append(otherUsageData, []string{service, lineItem, totalCostFormatted})
				}
			}

			// Add service total row
			if service == "Amazon Managed Streaming for Apache Kafka" {
				mskUsageData = append(mskUsageData, []string{"**TOTAL**", fmt.Sprintf("$%.2f", serviceTotalsFromCosts[service])})
			} else {
				otherUsageData = append(otherUsageData, []string{service, "**TOTAL**", fmt.Sprintf("$%.2f", serviceTotalsFromCosts[service])})
			}
		}

		// Add MSK section
		if len(mskUsageData) > 0 {
			mskUsageHeaders := []string{"Usage Type", "Total Cost (USD)"}
			md.AddHeading("Amazon Managed Streaming for Apache Kafka (MSK) Service Costs Summary", 3)
			md.AddParagraph("This section presents a summary of directly attributable costs for the Amazon Managed Streaming for Apache Kafka (MSK) service.")
			md.AddTable(mskUsageHeaders, mskUsageData)
		}

		// Add other services section
		if len(otherUsageData) > 0 {
			otherUsageHeaders := []string{"Service", "Usage Type", "Total Cost (USD)"}
			md.AddHeading("Other Services Costs Summary", 3)
			md.AddParagraph("This section details costs for additional AWS services. " +
				"Some portions of some costs may be indirectly attributable to Amazon MSK operations, while others may arise from unrelated service activities. " +
				"These *hidden* costs are not identifiable through AWS Cost APIs without comprehensive resource tagging across all resources used by MSK operations.")
			md.AddTable(otherUsageHeaders, otherUsageData)
		}

		// Add hidden costs section if we have data transfer costs
		if mskDataTransferCost > 0 && ec2DataTransferCost > 0 {
			md.AddHeading("Estimated Amazon MSK Hidden Costs", 3)
			md.AddParagraph("This section details potential *hidden* costs of Amazon MSK operations. " +
				"These are costs attributed to other AWS services, which may be caused by the operations of Amazon MSK, but are not directly attributable to the service itself.")

			hiddenHeaders := []string{"Hidden Cost Type", "Description", "Service", "Hidden Cost (USD)"}
			hiddenCosts := [][]string{
				{
					"Cross AZ Data Transfer costs",
					fmt.Sprintf("$%.2f of the $%.2f **EC2 - Other:DataTransfer-Regional-Bytes** cost is MSK-attributable (equivalent to **MSK:DataTransfer-Regional-Bytes** cost).", mskDataTransferCost, ec2DataTransferCost),
					"EC2 - Other",
					fmt.Sprintf("$%.2f", mskDataTransferCost),
				},
			}
			md.AddTable(hiddenHeaders, hiddenCosts)
		}

		md.AddHorizontalRule()
	}

	// Add build info section at the end
	md.AddHeading("KCP Build Info", 2)
	// We'll need to get build info from somewhere - let's add a simple version for now
	md.AddParagraph(fmt.Sprintf("**Version:** %s", build_info.Version))
	md.AddParagraph(fmt.Sprintf("**Commit:** %s", build_info.Commit))
	md.AddParagraph(fmt.Sprintf("**Date:** %s", build_info.Date))

	return md
}

// ProcessedMetric represents a single metric data point
type ProcessedMetric struct {
	Start string   `json:"start"`
	End   string   `json:"end"`
	Label string   `json:"label"`
	Value *float64 `json:"value"`
}

// ProcessedClusterMetrics represents the flattened metrics response
type ProcessedClusterMetrics struct {
	ClusterName string            `json:"cluster_name"`
	ClusterArn  string            `json:"cluster_arn"`
	Metrics     []ProcessedMetric `json:"metrics"`
	Metadata    MetricMetadata    `json:"metadata"`
}

type MetricsReport struct {
	ProcessedClusterMetrics []ProcessedClusterMetrics `json:"processed_cluster_metrics"`
}

func (m *MetricsReport) AsMarkdown() *markdown.Markdown {
	md := markdown.New()
	md.AddHeading("Amazon MSK Cluster Metrics Report", 1)

	md.AddParagraph("This report presents metrics analysis for Amazon MSK clusters across multiple regions.")

	// Process each cluster
	for _, clusterMetrics := range m.ProcessedClusterMetrics {
		md.AddHeading(fmt.Sprintf("Cluster: %s", clusterMetrics.ClusterName), 2)
		md.AddParagraph(fmt.Sprintf("This section presents metrics analysis for the cluster **%s**.", clusterMetrics.ClusterName))

		// Add metadata section
		md.AddHeading("Metrics Analysis Dimensions", 3)
		md.AddParagraph(fmt.Sprintf("**Cluster Name:** %s", clusterMetrics.ClusterName))
		md.AddParagraph(fmt.Sprintf("**Cluster ARN:** %s", clusterMetrics.ClusterArn))
		md.AddParagraph(fmt.Sprintf("**Cluster Type:** %s", clusterMetrics.Metadata.ClusterType))
		md.AddParagraph(fmt.Sprintf("**Kafka Version:** %s", clusterMetrics.Metadata.KafkaVersion))
		md.AddParagraph(fmt.Sprintf("**Enhanced Monitoring:** %s", clusterMetrics.Metadata.EnhancedMonitoring))
		md.AddParagraph(fmt.Sprintf("**Broker AZ Distribution:** %s", clusterMetrics.Metadata.BrokerAzDistribution))
		md.AddParagraph(fmt.Sprintf("**Follower Fetching:** %t", clusterMetrics.Metadata.FollowerFetching))
		md.AddParagraph(fmt.Sprintf("**Metrics Period:** %s to %s", clusterMetrics.Metadata.StartWindowDate, clusterMetrics.Metadata.EndWindowDate))
		md.AddParagraph(fmt.Sprintf("**Aggregation Period:** %d seconds", clusterMetrics.Metadata.Period))

		// Group metrics by label
		metricGroups := make(map[string][]ProcessedMetric)
		for _, metric := range clusterMetrics.Metrics {
			metricGroups[metric.Label] = append(metricGroups[metric.Label], metric)
		}

		// Sort metric names for consistent output across all sections
		sortedMetricNames := make([]string, 0, len(metricGroups))
		for metricName := range metricGroups {
			sortedMetricNames = append(sortedMetricNames, metricName)
		}
		// Simple sort
		for i := 0; i < len(sortedMetricNames)-1; i++ {
			for j := i + 1; j < len(sortedMetricNames); j++ {
				if sortedMetricNames[i] > sortedMetricNames[j] {
					sortedMetricNames[i], sortedMetricNames[j] = sortedMetricNames[j], sortedMetricNames[i]
				}
			}
		}

		// Create metrics summary table
		if len(metricGroups) > 0 {
			md.AddHeading("Metrics Summary", 3)
			md.AddParagraph("This section presents a summary of all collected metrics for this cluster.")

			// Create table data
			headers := []string{"Metric Name", "Latest Value", "Latest Period Start", "Latest Period End", "Data Points"}
			tableData := [][]string{}

			for _, metricName := range sortedMetricNames {
				metrics := metricGroups[metricName]

				// Find the latest non-null metric
				var latestMetric *ProcessedMetric
				for i := len(metrics) - 1; i >= 0; i-- {
					if metrics[i].Value != nil && metrics[i].Start != "" && metrics[i].End != "" {
						latestMetric = &metrics[i]
						break
					}
				}

				var latestValue, latestStart, latestEnd, dataPoints string
				dataPoints = fmt.Sprintf("%d", len(metrics))

				if latestMetric != nil {
					latestValue = fmt.Sprintf("%.6f", *latestMetric.Value)
					latestStart = latestMetric.Start
					latestEnd = latestMetric.End
				} else {
					latestValue = "No data"
					latestStart = "No data"
					latestEnd = "No data"
				}

				tableData = append(tableData, []string{
					metricName,
					latestValue,
					latestStart,
					latestEnd,
					dataPoints,
				})
			}

			md.AddTable(headers, tableData)
		}

		// Add detailed metrics section
		if len(metricGroups) > 0 {
			md.AddHeading("Detailed Metrics", 3)
			md.AddParagraph("This section presents detailed time-series data for each metric.")

			// Use the same sorted metric names from above
			for _, metricName := range sortedMetricNames {
				metrics := metricGroups[metricName]

				md.AddHeading(fmt.Sprintf("Metric: %s", metricName), 4)

				// Filter out null values for the detailed view
				validMetrics := []ProcessedMetric{}
				for _, metric := range metrics {
					if metric.Value != nil && metric.Start != "" && metric.End != "" {
						validMetrics = append(validMetrics, metric)
					}
				}

				if len(validMetrics) > 0 {
					detailHeaders := []string{"Period Start", "Period End", "Value"}
					detailData := [][]string{}

					for _, metric := range validMetrics {
						detailData = append(detailData, []string{
							metric.Start,
							metric.End,
							fmt.Sprintf("%.6f", *metric.Value),
						})
					}

					md.AddTable(detailHeaders, detailData)
				} else {
					md.AddParagraph("*No data available for this metric.*")
				}
			}
		}

		md.AddHorizontalRule()
	}

	// Add build info section at the end
	md.AddHeading("KCP Build Info", 2)
	md.AddParagraph(fmt.Sprintf("**Version:** %s", build_info.Version))
	md.AddParagraph(fmt.Sprintf("**Commit:** %s", build_info.Commit))
	md.AddParagraph(fmt.Sprintf("**Date:** %s", build_info.Date))

	return md
}
