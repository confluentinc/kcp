package region

import (
	"fmt"

	"github.com/confluentinc/kcp-internal/internal/services/markdown"
	"github.com/confluentinc/kcp-internal/internal/types"
)

// generateMarkdownReport creates a comprehensive markdown report of the scan results
func (rs *RegionScanner) generateMarkdownReport(scanResult *types.RegionScanResult, filePath string) error {
	md := markdown.New()

	// Title and overview
	md.AddHeading("MSK Region Scan Report", 1)
	md.AddParagraph(fmt.Sprintf("This report provides a comprehensive scan of the MSK environment in the **%s** region.", rs.region))
	md.AddParagraph(fmt.Sprintf("**Scan Timestamp:** %s", scanResult.Timestamp.Format("2006-01-02 15:04:05 UTC")))
	md.AddParagraph(fmt.Sprintf("**Total Clusters Found:** %d", len(scanResult.Clusters)))

	// Summary section
	md.AddHeading("Summary", 2)
	rs.addSummarySection(md, scanResult)

	// Clusters section
	md.AddHeading("Clusters", 2)
	rs.addClustersSection(md, scanResult)

	// Cluster ARNs section
	md.AddHeading("Cluster ARNs", 2)
	rs.addClusterArnsSection(md, scanResult)

	// Save to file
	return md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
}

// addSummarySection adds a summary of the scan results
func (rs *RegionScanner) addSummarySection(md *markdown.Markdown, scanResult *types.RegionScanResult) {
	// Count by cluster type
	provisionedCount := 0
	serverlessCount := 0
	for _, cluster := range scanResult.Clusters {
		switch cluster.Type {
		case "PROVISIONED":
			provisionedCount++
		case "SERVERLESS":
			serverlessCount++
		}
	}

	// Count by authentication type
	authTypes := make(map[string]int)
	for _, cluster := range scanResult.Clusters {
		authTypes[cluster.Authentication]++
	}

	// Count by status
	statusCounts := make(map[string]int)
	for _, cluster := range scanResult.Clusters {
		statusCounts[cluster.Status]++
	}

	summaryItems := []string{
		fmt.Sprintf("**Total Clusters:** %d", len(scanResult.Clusters)),
		fmt.Sprintf("**Provisioned Clusters:** %d", provisionedCount),
		fmt.Sprintf("**Serverless Clusters:** %d", serverlessCount),
		fmt.Sprintf("**VPC Connections:** %d", len(scanResult.VpcConnections)),
		fmt.Sprintf("**Kafka Configurations:** %d", len(scanResult.Configurations)),
		fmt.Sprintf("**Available Kafka Versions:** %d", len(scanResult.KafkaVersions)),
		fmt.Sprintf("**Replicators:** %d", len(scanResult.Replicators)),
	}

	md.AddList(summaryItems)
}

// addClustersSection adds the clusters table
func (rs *RegionScanner) addClustersSection(md *markdown.Markdown, scanResult *types.RegionScanResult) {
	headers := []string{"Cluster Name", "Status", "Type", "Authentication", "Public Access", "Encryption"}

	var tableData [][]string
	for _, cluster := range scanResult.Clusters {
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
func (rs *RegionScanner) addClusterArnsSection(md *markdown.Markdown, scanResult *types.RegionScanResult) {
	headers := []string{"Cluster Name", "Cluster ARN"}

	var tableData [][]string
	for _, cluster := range scanResult.Clusters {
		row := []string{
			cluster.ClusterName,
			cluster.ClusterARN,
		}
		tableData = append(tableData, row)
	}

	md.AddTable(headers, tableData)
}
