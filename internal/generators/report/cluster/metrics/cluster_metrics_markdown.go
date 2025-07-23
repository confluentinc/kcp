package metrics

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

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

	summaryItems := []string{}

	if cluster.ClusterMetricsSummary.AvgIngressThroughputMegabytesPerSecond != nil {
		summaryItems = append(summaryItems, fmt.Sprintf("**Average Ingress Throughput:** %.4f MB/s", *cluster.ClusterMetricsSummary.AvgIngressThroughputMegabytesPerSecond))
	}

	if cluster.ClusterMetricsSummary.PeakIngressThroughputMegabytesPerSecond != nil {
		summaryItems = append(summaryItems, fmt.Sprintf("**Peak Ingress Throughput:** %.4f MB/s", *cluster.ClusterMetricsSummary.PeakIngressThroughputMegabytesPerSecond))
	}

	if cluster.ClusterMetricsSummary.AvgEgressThroughputMegabytesPerSecond != nil {
		summaryItems = append(summaryItems, fmt.Sprintf("**Average Egress Throughput:** %.4f MB/s", *cluster.ClusterMetricsSummary.AvgEgressThroughputMegabytesPerSecond))
	}

	if cluster.ClusterMetricsSummary.PeakEgressThroughputMegabytesPerSecond != nil {
		summaryItems = append(summaryItems, fmt.Sprintf("**Peak Egress Throughput:** %.4f MB/s", *cluster.ClusterMetricsSummary.PeakEgressThroughputMegabytesPerSecond))
	}

	if cluster.ClusterMetricsSummary.Partitions != nil {
		summaryItems = append(summaryItems, fmt.Sprintf("**Total Partitions:** %.0f", *cluster.ClusterMetricsSummary.Partitions))
	}

	if cluster.ClusterMetricsSummary.InstanceType != nil {
		summaryItems = append(summaryItems, fmt.Sprintf("**Instance Type:** %s", *cluster.ClusterMetricsSummary.InstanceType))
	}

	if cluster.ClusterMetricsSummary.FollowerFetching != nil {
		followerStatus := "No"
		if *cluster.ClusterMetricsSummary.FollowerFetching {
			followerStatus = "Yes"
		}
		summaryItems = append(summaryItems, fmt.Sprintf("**Follower Fetching:** %s", followerStatus))
	}

	if cluster.ClusterMetricsSummary.TieredStorage != nil {
		tieredStatus := "No"
		if *cluster.ClusterMetricsSummary.TieredStorage {
			tieredStatus = "Yes"
		}
		summaryItems = append(summaryItems, fmt.Sprintf("**Tiered Storage:** %s", tieredStatus))
	}

	md.AddList(summaryItems)

	md.AddHeading("Paste these values into the TCO Calculator", 5)
	md.AddCodeBlock(
		fmt.Sprintf(
			"%.4f\n%.4f\n%.4f\n%.4f",
			*cluster.ClusterMetricsSummary.AvgIngressThroughputMegabytesPerSecond,
			*cluster.ClusterMetricsSummary.PeakIngressThroughputMegabytesPerSecond,
			*cluster.ClusterMetricsSummary.AvgEgressThroughputMegabytesPerSecond,
			*cluster.ClusterMetricsSummary.PeakEgressThroughputMegabytesPerSecond,
		), "json")
}

// addNodeDetails adds detailed node metrics
func (rm *ClusterMetricsCollector) addNodeDetails(md *markdown.Markdown, cluster types.ClusterMetrics) {
	md.AddHeading("Broker Details", 4)

	headers := []string{
		"Node ID",
		"Instance Type",
		"Avg Ingress (MB/s)",
		"Peak Ingress (MB/s)",
		"Avg Egress (MB/s)",
		"Peak Egress (MB/s)",
		"Avg Messages/s",
		"Peak Messages/s",
	}

	var tableData [][]string
	for _, node := range cluster.NodesMetrics {
		instanceType := "N/A"
		if node.InstanceType != nil {
			instanceType = *node.InstanceType
		}

		avgIngress := fmt.Sprintf("%.4f", node.BytesInPerSecAvg/1024/1024)
		peakIngress := fmt.Sprintf("%.4f", node.BytesInPerSecMax/1024/1024)
		avgEgress := fmt.Sprintf("%.4f", node.BytesOutPerSecAvg/1024/1024)
		peakEgress := fmt.Sprintf("%.4f", node.BytesOutPerSecMax/1024/1024)
		avgMessages := fmt.Sprintf("%.2f", node.MessagesInPerSecAvg)
		peakMessages := fmt.Sprintf("%.2f", node.MessagesInPerSecMax)

		row := []string{
			fmt.Sprintf("%d", node.NodeID),
			instanceType,
			avgIngress,
			peakIngress,
			avgEgress,
			peakEgress,
			avgMessages,
			peakMessages,
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}
