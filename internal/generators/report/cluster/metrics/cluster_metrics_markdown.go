package metrics

import (
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

func formatInstanceTypeOverride(instanceType *string) string {
	if instanceType == nil {
		return ""
	}
	s := *instanceType
	if idx := strings.Index(s, "."); idx != -1 && idx+1 < len(s) {
		s = s[idx+1:]
	}
	if len(s) > 0 {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	return s
}

// generateMarkdownReport creates a comprehensive markdown report of the region metrics
func (rm *ClusterMetricsCollector) generateMarkdownReport(metrics types.ClusterMetrics, filePath string) error {
	md := markdown.New()

	// Title and overview
	md.AddHeading("MSK Cluster Metrics Report", 1)
	md.AddParagraph(fmt.Sprintf("This report provides comprehensive metrics for MSK cluster **%s**.", rm.clusterArn))
	md.AddParagraph(fmt.Sprintf("**Report Period:** %s to %s", rm.startDate.Format("2006-01-02"), rm.endDate.Format("2006-01-02")))

	// Individual cluster sections
	md.AddHeading("Cluster Details", 2)
	rm.addIndividualClusterSections(md, metrics)

	// Save to file
	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
}

// addIndividualClusterSections adds individual sections for each cluster
func (rm *ClusterMetricsCollector) addIndividualClusterSections(md *markdown.Markdown, cluster types.ClusterMetrics) {
	// Add cluster heading
	md.AddHeading(cluster.ClusterName, 3)

	// Add cluster overview
	rm.addClusterOverview(md, cluster)

	// Add node details if available
	if len(cluster.NodesMetrics) > 0 {
		rm.addNodeDetails(md, cluster)
	}

	// Add cluster metrics summary
	rm.addClusterMetricsSummary(md, cluster)
}

// addClusterOverview adds basic cluster information
func (rm *ClusterMetricsCollector) addClusterOverview(md *markdown.Markdown, cluster types.ClusterMetrics) {
	md.AddHeading("Overview", 4)

	overviewItems := []string{
		fmt.Sprintf("**Cluster Type:** %s", cluster.ClusterType),
		fmt.Sprintf("**Number of Brokers:** %d", len(cluster.NodesMetrics)),
	}

	if cluster.KafkaVersion != nil {
		overviewItems = append(overviewItems, fmt.Sprintf("**Kafka Version:** %s", *cluster.KafkaVersion))
	}

	if cluster.EnhancedMonitoring != nil {
		overviewItems = append(overviewItems, fmt.Sprintf("**Enhanced Monitoring:** %s", *cluster.EnhancedMonitoring))
	}

	md.AddList(overviewItems)
}

// addClusterMetricsSummary adds cluster-level metrics summary
func (rm *ClusterMetricsCollector) addClusterMetricsSummary(md *markdown.Markdown, cluster types.ClusterMetrics) {
	md.AddHeading("Metrics Summary (TCO Calculator Inputs)", 4)

	rows := [][]string{

		// Avg Ingress Throughput (MB/s)
		{
			"Avg Ingress Throughput (MB/s)",
			func() string {
				if cluster.ClusterMetricsSummary.AvgIngressThroughputMegabytesPerSecond == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cluster.ClusterMetricsSummary.AvgIngressThroughputMegabytesPerSecond)
			}(),
		},
		// Peak Ingress Throughput (MB/s)
		{
			"Peak Ingress Throughput (MB/s)",
			func() string {
				if cluster.ClusterMetricsSummary.PeakIngressThroughputMegabytesPerSecond == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cluster.ClusterMetricsSummary.PeakIngressThroughputMegabytesPerSecond)
			}(),
		},
		// Avg Egress Throughput (MB/s)
		{
			"Avg Egress Throughput (MB/s)",
			func() string {
				if cluster.ClusterMetricsSummary.AvgEgressThroughputMegabytesPerSecond == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cluster.ClusterMetricsSummary.AvgEgressThroughputMegabytesPerSecond)
			}(),
		},
		// Peak Egress Throughput (MB/s)
		{
			"Peak Egress Throughput (MB/s)",
			func() string {
				if cluster.ClusterMetricsSummary.PeakEgressThroughputMegabytesPerSecond == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cluster.ClusterMetricsSummary.PeakEgressThroughputMegabytesPerSecond)
			}(),
		},
		// Retention (Days)
		{
			"Retention (Days)",
			func() string {
				if cluster.ClusterMetricsSummary.RetentionDays == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cluster.ClusterMetricsSummary.RetentionDays)
			}(),
		},
		// Partitions
		{
			"Partitions",
			func() string {
				if cluster.ClusterMetricsSummary.Partitions == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cluster.ClusterMetricsSummary.Partitions)
			}(),
		},
		// Replication Factor
		{
			"Replication Factor",
			func() string {
				if cluster.ClusterMetricsSummary.ReplicationFactor == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cluster.ClusterMetricsSummary.ReplicationFactor)
			}(),
		},
		// Follower Fetching
		{
			"Follower Fetching",
			func() string {
				if cluster.ClusterMetricsSummary.FollowerFetching == nil {
					return ""
				}
				if *cluster.ClusterMetricsSummary.FollowerFetching {
					return "TRUE"
				}
				return "FALSE"
			}(),
		},
		// Tiered Storage
		{
			"Tiered Storage",
			func() string {
				if cluster.ClusterMetricsSummary.TieredStorage == nil {
					return ""
				}
				if *cluster.ClusterMetricsSummary.TieredStorage {
					return "TRUE"
				}
				return "FALSE"
			}(),
		},
		// Local Retention in Primary Storage (Hrs) blank if TS = FALSE
		{
			"Local Retention in Primary Storage (Hrs)",
			func() string {
				if cluster.ClusterMetricsSummary.TieredStorage == nil || !*cluster.ClusterMetricsSummary.TieredStorage {
					return ""
				}
				if cluster.ClusterMetricsSummary.LocalRetentionInPrimaryStorageHours == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cluster.ClusterMetricsSummary.LocalRetentionInPrimaryStorageHours)
			}(),
		},
		// "Instance Type Override
		{
			"Instance Type Override",
			func() string {
				if cluster.ClusterMetricsSummary.InstanceType == nil {
					return ""
				}
				return formatInstanceTypeOverride(cluster.ClusterMetricsSummary.InstanceType)
			}(),
		},
	}

	md.AddTable([]string{"TCO Calculator Item", "Value (blank=unknown)"}, rows)

}

// addNodeDetails adds detailed node metrics
func (rm *ClusterMetricsCollector) addNodeDetails(md *markdown.Markdown, cluster types.ClusterMetrics) {
	md.AddHeading("Broker Details", 4)

	// Create headers with Node IDs as columns
	headers := []string{"Metric"}
	for _, node := range cluster.NodesMetrics {
		headers = append(headers, fmt.Sprintf("Node %d", node.NodeID))
	}

	// Define the metrics and their formatters
	metrics := []struct {
		name      string
		formatter func(node types.NodeMetrics) string
	}{
		{"Instance Type", func(node types.NodeMetrics) string {
			if node.InstanceType != nil {
				return *node.InstanceType
			}
			return "N/A"
		}},
		{"Volume Size (GB)", func(node types.NodeMetrics) string {
			if node.VolumeSizeGB != nil {
				return fmt.Sprintf("%d", *node.VolumeSizeGB)
			}
			return "N/A"
		}},
		{"Avg Ingress (MB/s)", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.4f", node.BytesInPerSecAvg/1024/1024)
		}},
		{"Peak Ingress (MB/s)", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.4f", node.BytesInPerSecMax/1024/1024)
		}},
		{"Avg Egress (MB/s)", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.4f", node.BytesOutPerSecAvg/1024/1024)
		}},
		{"Peak Egress (MB/s)", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.4f", node.BytesOutPerSecMax/1024/1024)
		}},
		{"Avg Messages/s", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.MessagesInPerSecAvg)
		}},
		{"Peak Messages/s", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.MessagesInPerSecMax)
		}},
		{"Avg Kafka Data Logs Disk Used (GB)", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.KafkaDataLogsDiskUsedAvg/1024/1024/1024)
		}},
		{"Peak Kafka Data Logs Disk Used (GB)", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.KafkaDataLogsDiskUsedMax/1024/1024/1024)
		}},
		{"Avg Remote Log Size (GB)", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.RemoteLogSizeBytesAvg/1024/1024/1024)
		}},
		{"Peak Remote Log Size (GB)", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.RemoteLogSizeBytesMax/1024/1024/1024)
		}},
		{"Peak Client Connection Count", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.ClientConnectionCountMax)
		}},
		{"Peak Partition Count", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.PartitionCountMax)
		}},
		{"Peak Global Topic Count", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.GlobalTopicCountMax)
		}},
		{"Peak Leader Count", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.LeaderCountMax)
		}},
		{"Peak Replication Bytes Out/s", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.ReplicationBytesOutPerSecMax/1024/1024)
		}},
		{"Peak Replication Bytes In/s", func(node types.NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.ReplicationBytesInPerSecMax/1024/1024)
		}},
	}

	// Build table data with metrics as rows
	var tableData [][]string
	for _, metric := range metrics {
		row := []string{metric.name}
		for _, node := range cluster.NodesMetrics {
			row = append(row, metric.formatter(node))
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}
