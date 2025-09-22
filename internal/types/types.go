package types

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/markdown"
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

// / todo review if we need
type GlobalMetrics struct {
	GlobalPartitionCountMax float64 `json:"global_partition_count_max"`
	GlobalTopicCountMax     float64 `json:"global_topic_count_max"`
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
	region := "us-east-1"

	// Process each region
	for _, regionCost := range c.ProcessedRegionCosts {
		md.AddHeading(fmt.Sprintf("Region: %s", region), 2)
		md.AddParagraph(fmt.Sprintf("This section presents cost analysis for the region **%s**.", region))

		// Add metadata section
		md.AddHeading("Cost Analysis Dimensions", 3)
		services := make([]string, 0, len(regionCost.Totals))
		for _, total := range regionCost.Totals {
			services = append(services, total.Service)
		}

		md.AddParagraph(fmt.Sprintf("**Region:** %s", region))
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

		for _, cost := range regionCost.Results {
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

// // ProcessedMetric represents a single metric data point
// type ProcessedMetric struct {
// 	Start string   `json:"start"`
// 	End   string   `json:"end"`
// 	Label string   `json:"label"`
// 	Value *float64 `json:"value"`
// }

// // ProcessedClusterMetrics represents the flattened metrics response
// type ProcessedClusterMetrics struct {
// 	ClusterName string            `json:"cluster_name"`
// 	ClusterArn  string            `json:"cluster_arn"`
// 	Metadata    MetricMetadata    `json:"metadata"`
// 	Metrics     []ProcessedMetric `json:"results"`
// }

type MetricsReport struct {
	ProcessedClusterMetrics []ProcessedClusterMetrics `json:"processed_cluster_metrics"`
}

func (m *MetricsReport) AsMarkdown() *markdown.Markdown {
	md := markdown.New()
	md.AddHeading("Amazon MSK Cluster Metrics Report", 1)

	md.AddParagraph("This report presents metrics analysis for Amazon MSK clusters across multiple regions.")

	cluster := "cluster-1"
	clusterArn := "arn:aws:kafka:us-east-1:123456789012:cluster/cluster-1/123456789012"

	// Process each cluster
	for _, clusterMetrics := range m.ProcessedClusterMetrics {
		md.AddHeading(fmt.Sprintf("Cluster: %s", cluster), 2)
		md.AddParagraph(fmt.Sprintf("This section presents metrics analysis for the cluster **%s**.", cluster))

		// Add metadata section
		md.AddHeading("Metrics Analysis Dimensions", 3)
		md.AddParagraph(fmt.Sprintf("**Cluster Name:** %s", cluster))
		md.AddParagraph(fmt.Sprintf("**Cluster ARN:** %s", clusterArn))
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

// type ProcessedDiscovery struct {
// 	Regions      []ProcessedDiscoveredRegion `json:"regions"`
// 	KcpBuildInfo KcpBuildInfo                `json:"kcp_build_info"`
// 	Timestamp    time.Time                   `json:"timestamp"`
// }

// type ProcessedDiscoveredRegion struct {
// 	Name           string                                      `json:"name"`
// 	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
// 	Costs          ProcessedCostInformation                    `json:"costs"`
// 	Clusters       []ProcessDiscoveredCluster                  `json:"clusters"`
// 	// internal only - exclude from JSON output
// 	ClusterArns []string `json:"-"`
// }

// type ProcessedCostInformation struct {
// 	CostMetadata         CostMetadata         `json:"metadata"`
// 	ProcessedRegionCosts ProcessedRegionCosts `json:"results"`
// }

// type ProcessDiscoveredCluster struct {
// 	ClusterName string                  `json:"cluster_name"`
// 	ClusterArn  string                  `json:"cluster_arn"`
// 	Metrics     ProcessedClusterMetrics `json:"metrics"`
// }

// type ProcessedClusterMetrics struct {
// 	MetricMetadata MetricMetadata                     `json:"metadata"`
// 	Results        []cloudwatchtypes.MetricDataResult `json:"results"`
// }
