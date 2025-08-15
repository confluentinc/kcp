package types

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/services/markdown"
)

// RegionScanResult contains the results of scanning an AWS region for MSK resources
type RegionScanResult struct {
	Timestamp      time.Time                                   `json:"timestamp"`
	Clusters       []ClusterSummary                            `json:"clusters"`
	VpcConnections []kafkatypes.VpcConnection                  `json:"vpc_connections"`
	Configurations []kafka.DescribeConfigurationRevisionOutput `json:"configurations"`
	KafkaVersions  []kafkatypes.KafkaVersion                   `json:"kafka_versions"`
	Replicators    []kafka.DescribeReplicatorOutput            `json:"replicators"`
	Region         string                                      `json:"region"`
}

func (rs *RegionScanResult) WriteAsJson(filePath string) error {
	data, err := rs.AsJson()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("❌ Failed to write file: %v", err)
	}

	return nil
}

func (rs *RegionScanResult) AsJson() ([]byte, error ){
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to marshal scan results: %v", err)
	}
	return data, nil
}

func (rs *RegionScanResult) WriteAsMarkdown(filePath string) (error) {
	md := rs.AsMarkdown()
	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
}

// generateMarkdownReport creates a comprehensive markdown report of the scan results
func (rs *RegionScanResult) AsMarkdown() (*markdown.Markdown) {
	md := markdown.New()

	// Title and overview
	md.AddHeading("MSK Region Scan Report", 1)
	md.AddParagraph(fmt.Sprintf("This report provides a comprehensive scan of the MSK environment in the **%s** region.", rs.Region))
	md.AddParagraph(fmt.Sprintf("**Scan Timestamp:** %s", rs.Timestamp.Format("2006-01-02 15:04:05 UTC")))
	md.AddParagraph(fmt.Sprintf("**Total Clusters Found:** %d", len(rs.Clusters)))

	// Summary section
	md.AddHeading("Summary", 2)
	rs.addSummarySection(md)

	// Clusters section
	md.AddHeading("Clusters", 2)
	rs.addClustersSection(md)

	// Cluster ARNs section
	md.AddHeading("Cluster ARNs", 2)
	rs.addClusterArnsSection(md)

	// Save to file
	return md
}

// addSummarySection adds a summary of the scan results
func (rs *RegionScanResult) addSummarySection(md *markdown.Markdown) {
	// Count by cluster type
	provisionedCount := 0
	serverlessCount := 0
	for _, cluster := range rs.Clusters {
		switch cluster.Type {
		case "PROVISIONED":
			provisionedCount++
		case "SERVERLESS":
			serverlessCount++
		}
	}

	// Count by authentication type
	authTypes := make(map[string]int)
	for _, cluster := range rs.Clusters {
		authTypes[cluster.Authentication]++
	}

	// Count by status
	statusCounts := make(map[string]int)
	for _, cluster := range rs.Clusters {
		statusCounts[cluster.Status]++
	}

	summaryItems := []string{
		fmt.Sprintf("**Total Clusters:** %d", len(rs.Clusters)),
		fmt.Sprintf("**Provisioned Clusters:** %d", provisionedCount),
		fmt.Sprintf("**Serverless Clusters:** %d", serverlessCount),
		fmt.Sprintf("**VPC Connections:** %d", len(rs.VpcConnections)),
		fmt.Sprintf("**Kafka Configurations:** %d", len(rs.Configurations)),
		fmt.Sprintf("**Available Kafka Versions:** %d", len(rs.KafkaVersions)),
		fmt.Sprintf("**Replicators:** %d", len(rs.Replicators)),
	}

	md.AddList(summaryItems)
}

// addClustersSection adds the clusters table
func (rs *RegionScanResult) addClustersSection(md *markdown.Markdown) {
	headers := []string{"Cluster Name", "Status", "Type", "Authentication", "Public Access", "Encryption"}

	var tableData [][]string
	for _, cluster := range rs.Clusters {
		publicAccess := "No"
		if cluster.PublicAccess {
			publicAccess = "Yes"
		}

		row := []string{
			cluster.ClusterName,
			cluster.Status,
			cluster.Type,
			cluster.Authentication,
			publicAccess,
			string(cluster.ClientBrokerEncryptionInTransit),
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}

// addClusterArnsSection adds the cluster ARNs table
func (rs *RegionScanResult) addClusterArnsSection(md *markdown.Markdown) {
	headers := []string{"Cluster Name", "Cluster ARN"}

	var tableData [][]string
	for _, cluster := range rs.Clusters {
		row := []string{
			cluster.ClusterName,
			cluster.ClusterARN,
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}
