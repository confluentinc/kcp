package types

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/services/markdown"
)

type ClusterMetrics struct {
	Region                string                `json:"region"`
	ClusterArn            string                `json:"cluster_arn"`
	ClusterName           string                `json:"cluster_name"`
	ClusterType           string                `json:"cluster_type"`
	BrokerAZDistribution  *string               `json:"broker_az_distribution"`
	Authentication        map[string]any        `json:"authentication"`
	KafkaVersion          *string               `json:"kafka_version"`
	EnhancedMonitoring    *string               `json:"enhanced_monitoring"`
	StartDate             time.Time             `json:"start_date"`
	EndDate               time.Time             `json:"end_date"`
	NodesMetrics          []NodeMetrics         `json:"nodes"`
	GlobalMetrics         GlobalMetrics         `json:"global_metrics"`
	ClusterMetricsSummary ClusterMetricsSummary `json:"cluster_metrics_summary"`
}

type GlobalMetrics struct {
	GlobalPartitionCountMax float64 `json:"global_partition_count_max"`
	GlobalTopicCountMax     float64 `json:"global_topic_count_max"`
}

type NodeMetrics struct {
	NodeID                       int     `json:"node_id"`
	InstanceType                 *string `json:"instance_type"`
	VolumeSizeGB                 *int    `json:"volume_size_gb"`
	BytesInPerSecAvg             float64 `json:"bytes_in_per_sec_avg"`
	BytesOutPerSecAvg            float64 `json:"bytes_out_per_sec_avg"`
	MessagesInPerSecAvg          float64 `json:"messages_in_per_sec_avg"`
	KafkaDataLogsDiskUsedAvg     float64 `json:"kafka_data_logs_disk_used_avg"`
	RemoteLogSizeBytesAvg        float64 `json:"remote_log_size_bytes_avg"`
	BytesInPerSecMax             float64 `json:"bytes_in_per_sec_max"`
	BytesOutPerSecMax            float64 `json:"bytes_out_per_sec_max"`
	MessagesInPerSecMax          float64 `json:"messages_in_per_sec_max"`
	KafkaDataLogsDiskUsedMax     float64 `json:"kafka_data_logs_disk_used_max"`
	RemoteLogSizeBytesMax        float64 `json:"remote_log_size_bytes_max"`
	ClientConnectionCountMax     float64 `json:"client_connection_count_max"`
	PartitionCountMax            float64 `json:"partition_count_max"`
	LeaderCountMax               float64 `json:"leader_count_max"`
	ReplicationBytesOutPerSecMax float64 `json:"replication_bytes_out_per_sec_max"`
	ReplicationBytesInPerSecMax  float64 `json:"replication_bytes_in_per_sec_max"`
}

type ClusterMetricsSummary struct {
	AvgIngressThroughputMegabytesPerSecond  *float64 `json:"avg_ingress_throughput_megabytes_per_second"`
	PeakIngressThroughputMegabytesPerSecond *float64 `json:"peak_ingress_throughput_megabytes_per_second"`
	AvgEgressThroughputMegabytesPerSecond   *float64 `json:"avg_egress_throughput_megabytes_per_second"`
	PeakEgressThroughputMegabytesPerSecond  *float64 `json:"peak_egress_throughput_megabytes_per_second"`
	RetentionDays                           *float64 `json:"retention_days,omitempty"`
	Partitions                              *float64 `json:"partitions"`
	ReplicationFactor                       *float64 `json:"replication_factor,omitempty"`
	FollowerFetching                        *bool    `json:"follower_fetching"`
	TieredStorage                           *bool    `json:"tiered_storage"`
	LocalRetentionInPrimaryStorageHours     *float64 `json:"local_retention_in_primary_storage_hours,omitempty"`
	InstanceType                            *string  `json:"instance_type"`
}

func (cm *ClusterMetrics) GetJsonPath() string {
	return filepath.Join(cm.GetDirPath(), fmt.Sprintf("%s-metrics.json", aws.ToString(&cm.ClusterName)))
}

func (cm *ClusterMetrics) GetMarkdownPath() string {
	return filepath.Join(cm.GetDirPath(), fmt.Sprintf("%s-metrics.md", aws.ToString(&cm.ClusterName)))
}

func (cm *ClusterMetrics) GetDirPath() string {
	return cm.GetDirPathWithBase("kcp-scan")
}

func (cm *ClusterMetrics) GetDirPathWithBase(baseDir string) string {
	return filepath.Join(baseDir, cm.Region, aws.ToString(&cm.ClusterName))
}

func (cm *ClusterMetrics) WriteAsJson() error {
	return cm.WriteAsJsonWithBase("kcp-scan")
}

func (cm *ClusterMetrics) WriteAsJsonWithBase(baseDir string) error {
	dirPath := cm.GetDirPathWithBase(baseDir)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := filepath.Join(dirPath, fmt.Sprintf("%s-metrics.json", aws.ToString(&cm.ClusterName)))

	data, err := cm.AsJson()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("❌ Failed to write file: %v", err)
	}

	return nil
}

func (cm *ClusterMetrics) AsJson() ([]byte, error) {
	data, err := json.MarshalIndent(cm, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to marshal scan results: %v", err)
	}
	return data, nil
}

func (cm *ClusterMetrics) WriteAsMarkdown(suppressToTerminal bool) error {
	return cm.WriteAsMarkdownWithBase("kcp-scan", suppressToTerminal)
}

func (cm *ClusterMetrics) WriteAsMarkdownWithBase(baseDir string, suppressToTerminal bool) error {
	dirPath := cm.GetDirPathWithBase(baseDir)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := filepath.Join(dirPath, fmt.Sprintf("%s-metrics.md", aws.ToString(&cm.ClusterName)))
	md := cm.AsMarkdown()
	return md.Print(markdown.PrintOptions{ToTerminal: !suppressToTerminal, ToFile: filePath})
}

// generateMarkdownReport creates a comprehensive markdown report of the scan results
func (cm *ClusterMetrics) AsMarkdown() *markdown.Markdown {
	md := markdown.New()

	// Title and overview
	md.AddHeading("MSK Cluster Metrics Report", 1)
	md.AddParagraph(fmt.Sprintf("This report provides comprehensive metrics for MSK cluster **%s**.", cm.ClusterArn))
	md.AddParagraph(fmt.Sprintf("**Report Period:** %s to %s", cm.StartDate.Format("2006-01-02"), cm.EndDate.Format("2006-01-02")))

	// Individual cluster sections
	md.AddHeading("Cluster Details", 2)
	cm.addIndividualClusterSections(md)

	return md
}

// addIndividualClusterSections adds individual sections for each cluster
func (cm *ClusterMetrics) addIndividualClusterSections(md *markdown.Markdown) {
	// Add cluster heading
	md.AddHeading(cm.ClusterName, 3)

	// Add cluster overview
	cm.addClusterOverview(md)

	// Add global metrics
	cm.addGlobalMetricsRows(md)

	// Add node details if available
	if len(cm.NodesMetrics) > 0 {
		cm.addNodeDetails(md)
	}

	// Add cluster metrics summary
	cm.addClusterMetricsSummary(md)
}

// addClusterOverview adds basic cluster information
func (cm *ClusterMetrics) addClusterOverview(md *markdown.Markdown) {
	md.AddHeading("Overview", 4)

	overviewItems := []string{
		fmt.Sprintf("**Cluster Type:** %s", cm.ClusterType),
		fmt.Sprintf("**Number of Brokers:** %d", len(cm.NodesMetrics)),
	}

	if cm.KafkaVersion != nil {
		overviewItems = append(overviewItems, fmt.Sprintf("**Kafka Version:** %s", *cm.KafkaVersion))
	}

	if cm.EnhancedMonitoring != nil {
		overviewItems = append(overviewItems, fmt.Sprintf("**Enhanced Monitoring:** %s", *cm.EnhancedMonitoring))
	}

	md.AddList(overviewItems)
}

// addClusterMetricsSummary adds cluster-level metrics summary
func (cm *ClusterMetrics) addClusterMetricsSummary(md *markdown.Markdown) {
	md.AddHeading("Metrics Summary (TCO Calculator Inputs)", 4)

	rows := [][]string{

		// Avg Ingress Throughput (MB/s)
		{
			"Avg Ingress Throughput (MB/s)",
			func() string {
				if cm.ClusterMetricsSummary.AvgIngressThroughputMegabytesPerSecond == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cm.ClusterMetricsSummary.AvgIngressThroughputMegabytesPerSecond)
			}(),
		},
		// Peak Ingress Throughput (MB/s)
		{
			"Peak Ingress Throughput (MB/s)",
			func() string {
				if cm.ClusterMetricsSummary.PeakIngressThroughputMegabytesPerSecond == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cm.ClusterMetricsSummary.PeakIngressThroughputMegabytesPerSecond)
			}(),
		},
		// Avg Egress Throughput (MB/s)
		{
			"Avg Egress Throughput (MB/s)",
			func() string {
				if cm.ClusterMetricsSummary.AvgEgressThroughputMegabytesPerSecond == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cm.ClusterMetricsSummary.AvgEgressThroughputMegabytesPerSecond)
			}(),
		},
		// Peak Egress Throughput (MB/s)
		{
			"Peak Egress Throughput (MB/s)",
			func() string {
				if cm.ClusterMetricsSummary.PeakEgressThroughputMegabytesPerSecond == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cm.ClusterMetricsSummary.PeakEgressThroughputMegabytesPerSecond)
			}(),
		},
		// Retention (Days)
		{
			"Retention (Days)",
			func() string {
				if cm.ClusterMetricsSummary.RetentionDays == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cm.ClusterMetricsSummary.RetentionDays)
			}(),
		},
		// Partitions
		{
			"Partitions",
			func() string {
				if cm.ClusterMetricsSummary.Partitions == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cm.ClusterMetricsSummary.Partitions)
			}(),
		},
		// Replication Factor
		{
			"Replication Factor",
			func() string {
				if cm.ClusterMetricsSummary.ReplicationFactor == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cm.ClusterMetricsSummary.ReplicationFactor)
			}(),
		},
		// Follower Fetching
		{
			"Follower Fetching",
			func() string {
				if cm.ClusterMetricsSummary.FollowerFetching == nil {
					return ""
				}
				if *cm.ClusterMetricsSummary.FollowerFetching {
					return "TRUE"
				}
				return "FALSE"
			}(),
		},
		// Tiered Storage
		{
			"Tiered Storage",
			func() string {
				if cm.ClusterMetricsSummary.TieredStorage == nil {
					return ""
				}
				if *cm.ClusterMetricsSummary.TieredStorage {
					return "TRUE"
				}
				return "FALSE"
			}(),
		},
		// Local Retention in Primary Storage (Hrs) blank if TS = FALSE
		{
			"Local Retention in Primary Storage (Hrs)",
			func() string {
				if cm.ClusterMetricsSummary.TieredStorage == nil || !*cm.ClusterMetricsSummary.TieredStorage {
					return ""
				}
				if cm.ClusterMetricsSummary.LocalRetentionInPrimaryStorageHours == nil {
					return ""
				}
				return fmt.Sprintf("%.4f", *cm.ClusterMetricsSummary.LocalRetentionInPrimaryStorageHours)
			}(),
		},
		// "Instance Type Override
		{
			"Instance Type Override",
			func() string {
				if cm.ClusterMetricsSummary.InstanceType == nil {
					return ""
				}
				return formatInstanceTypeOverride(cm.ClusterMetricsSummary.InstanceType)
			}(),
		},
	}

	md.AddTable([]string{"TCO Calculator Item", "Value (blank=unknown)"}, rows)

}

func (cm *ClusterMetrics) addGlobalMetricsRows(md *markdown.Markdown) {

	md.AddHeading("Cluster Metrics", 4)

	// Create headers with Node IDs as columns
	headers := []string{"Metric", ""}

	// Define the metrics and their formatters
	metrics := []struct {
		name      string
		formatter func(gm GlobalMetrics) string
	}{
		{"Global Partition Count", func(gm GlobalMetrics) string {
			return fmt.Sprintf("%.2f", gm.GlobalPartitionCountMax)
		}},
		{"Global Topic Count", func(gm GlobalMetrics) string {
			return fmt.Sprintf("%.2f", gm.GlobalTopicCountMax)
		}},
	}

	// Build table data with metrics as rows
	var tableData [][]string
	for _, metric := range metrics {
		row := []string{metric.name, metric.formatter(cm.GlobalMetrics)}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)

}

// addNodeDetails adds detailed node metrics
func (cm *ClusterMetrics) addNodeDetails(md *markdown.Markdown) {
	md.AddHeading("Broker Metrics", 4)

	// Create headers with Node IDs as columns
	headers := []string{"Metric"}
	for _, node := range cm.NodesMetrics {
		headers = append(headers, fmt.Sprintf("Node %d", node.NodeID))
	}

	// Define the metrics and their formatters
	metrics := []struct {
		name      string
		formatter func(node NodeMetrics) string
	}{
		{"Instance Type", func(node NodeMetrics) string {
			if node.InstanceType != nil {
				return *node.InstanceType
			}
			return "N/A"
		}},
		{"Volume Size (GB)", func(node NodeMetrics) string {
			if node.VolumeSizeGB != nil {
				return fmt.Sprintf("%d", *node.VolumeSizeGB)
			}
			return "N/A"
		}},
		{"Avg Ingress (MB/s)", func(node NodeMetrics) string {
			return fmt.Sprintf("%.4f", node.BytesInPerSecAvg/1024/1024)
		}},
		{"Peak Ingress (MB/s)", func(node NodeMetrics) string {
			return fmt.Sprintf("%.4f", node.BytesInPerSecMax/1024/1024)
		}},
		{"Avg Egress (MB/s)", func(node NodeMetrics) string {
			return fmt.Sprintf("%.4f", node.BytesOutPerSecAvg/1024/1024)
		}},
		{"Peak Egress (MB/s)", func(node NodeMetrics) string {
			return fmt.Sprintf("%.4f", node.BytesOutPerSecMax/1024/1024)
		}},
		{"Avg Messages/s", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.MessagesInPerSecAvg)
		}},
		{"Peak Messages/s", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.MessagesInPerSecMax)
		}},
		{"Avg Kafka Data Logs Disk Used (GB)", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.KafkaDataLogsDiskUsedAvg/1024/1024/1024)
		}},
		{"Peak Kafka Data Logs Disk Used (GB)", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.KafkaDataLogsDiskUsedMax/1024/1024/1024)
		}},
		{"Avg Remote Log Size (GB)", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.RemoteLogSizeBytesAvg/1024/1024/1024)
		}},
		{"Peak Remote Log Size (GB)", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.RemoteLogSizeBytesMax/1024/1024/1024)
		}},
		{"Peak Client Connection Count", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.ClientConnectionCountMax)
		}},
		{"Peak Partition Count", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.PartitionCountMax)
		}},
		{"Peak Leader Count", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.LeaderCountMax)
		}},
		{"Peak Replication Bytes Out/s", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.ReplicationBytesOutPerSecMax/1024/1024)
		}},
		{"Peak Replication Bytes In/s", func(node NodeMetrics) string {
			return fmt.Sprintf("%.2f", node.ReplicationBytesInPerSecMax/1024/1024)
		}},
	}

	// Build table data with metrics as rows
	var tableData [][]string
	for _, metric := range metrics {
		row := []string{metric.name}
		for _, node := range cm.NodesMetrics {
			row = append(row, metric.formatter(node))
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}

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
