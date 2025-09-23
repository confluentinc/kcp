package report

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

type ReporterOpts struct {
	State types.State
}

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState
}

type Reporter struct {
	ReportService   ReportService
	MarkdownService markdown.Markdown
	State           types.State
}

func NewReporter(reportService ReportService, markdownService markdown.Markdown, opts ReporterOpts) *Reporter {
	return &Reporter{
		ReportService:   reportService,
		MarkdownService: markdownService,
		State:           opts.State,
	}
}

func (r *Reporter) Run() error {
	slog.Info("🔍 running reporter")
	if err := r.generateReport(r.State); err != nil {
		return fmt.Errorf("failed to generate report: %v", err)
	}

	return nil
}

func (r *Reporter) generateReport(state types.State) error {
	processedState := r.ReportService.ProcessState(state)

	// Generate unified markdown report
	markdownReport := r.generateUnifiedMarkdownReport(processedState)

	// Write the unified report to report.md
	if err := markdownReport.Print(markdown.PrintOptions{
		ToTerminal: false,
		ToFile:     "report.md",
	}); err != nil {
		return fmt.Errorf("failed to write markdown report: %v", err)
	}

	return nil
}

func (r *Reporter) generateUnifiedMarkdownReport(processedState types.ProcessedState) *markdown.Markdown {
	md := markdown.New()
	md.AddHeading("KCP AWS Infrastructure Report", 1)
	md.AddParagraph("This report presents a comprehensive analysis of AWS MSK infrastructure including cost analysis and cluster metrics across multiple regions.")

	// Process each region
	for _, region := range processedState.Regions {
		md.AddHeading(fmt.Sprintf("Region: %s", region.Name), 2)
		md.AddParagraph(fmt.Sprintf("This section presents analysis for the region **%s**.", region.Name))

		// Add cost analysis section
		r.addRegionCostAnalysis(md, region)

		// Add cluster metrics section
		r.addRegionClusterMetrics(md, region)

		md.AddHorizontalRule()
	}

	// Add build info section at the end
	md.AddHeading("KCP Build Info", 2)
	md.AddParagraph(fmt.Sprintf("**Version:** %s", build_info.Version))
	md.AddParagraph(fmt.Sprintf("**Commit:** %s", build_info.Commit))
	md.AddParagraph(fmt.Sprintf("**Date:** %s", build_info.Date))

	return md
}

func (r *Reporter) addRegionCostAnalysis(md *markdown.Markdown, region types.ProcessedRegion) {
	regionCost := region.Costs

	md.AddHeading("Cost Analysis", 3)
	md.AddParagraph(fmt.Sprintf("This section presents cost analysis for the region **%s**.", region.Name))

	// Add metadata section
	md.AddHeading("Cost Analysis Dimensions", 4)
	services := make([]string, 0, len(regionCost.Totals))
	for _, total := range regionCost.Totals {
		services = append(services, total.Service)
	}

	md.AddParagraph(fmt.Sprintf("**Region:** %s", region.Name))
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
		md.AddHeading("Amazon Managed Streaming for Apache Kafka (MSK) Service Costs Summary", 4)
		md.AddParagraph("This section presents a summary of directly attributable costs for the Amazon Managed Streaming for Apache Kafka (MSK) service.")
		md.AddTable(mskUsageHeaders, mskUsageData)
	}

	// Add other services section
	if len(otherUsageData) > 0 {
		otherUsageHeaders := []string{"Service", "Usage Type", "Total Cost (USD)"}
		md.AddHeading("Other Services Costs Summary", 4)
		md.AddParagraph("This section details costs for additional AWS services. " +
			"Some portions of some costs may be indirectly attributable to Amazon MSK operations, while others may arise from unrelated service activities. " +
			"These *hidden* costs are not identifiable through AWS Cost APIs without comprehensive resource tagging across all resources used by MSK operations.")
		md.AddTable(otherUsageHeaders, otherUsageData)
	}

	// Add hidden costs section if we have data transfer costs
	if mskDataTransferCost > 0 && ec2DataTransferCost > 0 {
		md.AddHeading("Estimated Amazon MSK Hidden Costs", 4)
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
}

func (r *Reporter) addRegionClusterMetrics(md *markdown.Markdown, region types.ProcessedRegion) {
	if len(region.Clusters) == 0 {
		md.AddHeading("Cluster Metrics", 3)
		md.AddParagraph("No clusters found in this region.")
		return
	}

	md.AddHeading("Cluster Metrics Summary", 3)
	md.AddParagraph(fmt.Sprintf("This section presents metrics analysis for **%d** cluster(s) in the region **%s**.", len(region.Clusters), region.Name))

	// Create clusters summary table
	summaryHeaders := []string{"Cluster Name", "Cluster Type", "Kafka Version", "Enhanced Monitoring", "Broker AZ Distribution"}
	summaryData := [][]string{}

	for _, cluster := range region.Clusters {
		summaryData = append(summaryData, []string{
			cluster.Name,
			cluster.ClusterMetrics.Metadata.ClusterType,
			cluster.ClusterMetrics.Metadata.KafkaVersion,
			cluster.ClusterMetrics.Metadata.EnhancedMonitoring,
			cluster.ClusterMetrics.Metadata.BrokerAzDistribution,
		})
	}

	md.AddTable(summaryHeaders, summaryData)

	// Process each cluster in detail
	for _, cluster := range region.Clusters {
		md.AddHeading(fmt.Sprintf("Cluster: %s", cluster.Name), 4)
		md.AddParagraph(fmt.Sprintf("This section presents detailed metrics analysis for the cluster **%s**.", cluster.Name))

		clusterMetrics := cluster.ClusterMetrics

		// Add metadata section
		md.AddHeading("Metrics Analysis Dimensions", 5)
		md.AddParagraph(fmt.Sprintf("**Cluster Name:** %s", cluster.Name))
		md.AddParagraph(fmt.Sprintf("**Cluster ARN:** %s", cluster.Arn))
		md.AddParagraph(fmt.Sprintf("**Cluster Type:** %s", clusterMetrics.Metadata.ClusterType))
		md.AddParagraph(fmt.Sprintf("**Kafka Version:** %s", clusterMetrics.Metadata.KafkaVersion))
		md.AddParagraph(fmt.Sprintf("**Enhanced Monitoring:** %s", clusterMetrics.Metadata.EnhancedMonitoring))
		md.AddParagraph(fmt.Sprintf("**Broker AZ Distribution:** %s", clusterMetrics.Metadata.BrokerAzDistribution))
		md.AddParagraph(fmt.Sprintf("**Follower Fetching:** %t", clusterMetrics.Metadata.FollowerFetching))
		md.AddParagraph(fmt.Sprintf("**Metrics Period:** %s to %s", clusterMetrics.Metadata.StartWindowDate, clusterMetrics.Metadata.EndWindowDate))
		md.AddParagraph(fmt.Sprintf("**Aggregation Period:** %d seconds", clusterMetrics.Metadata.Period))

		// Group metrics by label
		metricGroups := make(map[string][]types.ProcessedMetric)
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
			md.AddHeading("Metrics Summary", 5)
			md.AddParagraph("This section presents a summary of all collected metrics for this cluster.")

			// Create table data
			headers := []string{"Metric Name", "Latest Value", "Latest Period Start", "Latest Period End", "Data Points"}
			tableData := [][]string{}

			for _, metricName := range sortedMetricNames {
				metrics := metricGroups[metricName]

				// Find the latest non-null metric
				var latestMetric *types.ProcessedMetric
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
	}
}
