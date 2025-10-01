package metrics

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState

	// will need to become clusterArn
	FilterClusterMetrics(processedState types.ProcessedState, clusterArn string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error)
}

type MetricReporterOpts struct {
	ClusterArns []string
	State       *types.State
	StartDate   time.Time
	EndDate     time.Time
}

type MetricReporter struct {
	reportService ReportService

	clusterArns []string
	state       *types.State
	startDate   time.Time
	endDate     time.Time
}

func NewMetricReporter(reportService ReportService, opts MetricReporterOpts) *MetricReporter {
	return &MetricReporter{
		reportService: reportService,

		clusterArns: opts.ClusterArns,
		state:       opts.State,
		startDate:   opts.StartDate,
		endDate:     opts.EndDate,
	}
}

func (r *MetricReporter) Run() error {
	slog.Info("🔍 processing clusters", "clusters", r.clusterArns, "startDate", r.startDate, "endDate", r.endDate)

	processedState := r.reportService.ProcessState(*r.state)
	processedClusterMetrics := []types.ProcessedClusterMetrics{}

	// find the clusters in the state

	for _, clusterArn := range r.clusterArns {
		clusterMetrics, err := r.reportService.FilterClusterMetrics(processedState, clusterArn, &r.startDate, &r.endDate)
		if err != nil {
			return fmt.Errorf("failed to filter cluster metrics: %v", err)
		}
		processedClusterMetrics = append(processedClusterMetrics, *clusterMetrics)
	}

	fileName := fmt.Sprintf("metric_report_%s.md", time.Now().Format("2006-01-02_15-04-05"))
	markdownReport := r.generateReport(processedClusterMetrics)
	if err := markdownReport.Print(markdown.PrintOptions{ToTerminal: false, ToFile: fileName}); err != nil {
		return fmt.Errorf("failed to write markdown report: %v", err)
	}

	return nil
}

func (r *MetricReporter) generateReport(processedClusterMetrics []types.ProcessedClusterMetrics) *markdown.Markdown {
	md := markdown.New()

	md.AddHeading("AWS Metrics Report", 1)

	md.AddParagraph(fmt.Sprintf("**Report Period:** %s to %s",
		r.startDate.Format("2006-01-02"),
		r.endDate.Format("2006-01-02")))

	regionGroups := r.groupClustersByRegion(processedClusterMetrics)

	regions := make([]string, 0, len(regionGroups))
	for region := range regionGroups {
		regions = append(regions, region)
	}
	md.AddParagraph(fmt.Sprintf("**Regions:** %v", regions))

	if len(processedClusterMetrics) > 0 {
		metadata := processedClusterMetrics[0].Metadata
		md.AddParagraph(fmt.Sprintf("**Cluster Type:** %s", metadata.ClusterType))
		md.AddParagraph(fmt.Sprintf("**Kafka Version:** %s", metadata.KafkaVersion))
		md.AddParagraph(fmt.Sprintf("**Enhanced Monitoring:** %s", metadata.EnhancedMonitoring))
		md.AddParagraph(fmt.Sprintf("**Period:** %d seconds", metadata.Period))
	}

	md.AddHorizontalRule()

	for region, clusters := range regionGroups {
		r.addRegionSection(md, region, clusters)
	}

	return md
}

func (r *MetricReporter) groupClustersByRegion(processedClusterMetrics []types.ProcessedClusterMetrics) map[string][]types.ProcessedClusterMetrics {
	regionGroups := make(map[string][]types.ProcessedClusterMetrics)

	for i, clusterMetrics := range processedClusterMetrics {
		// Extract region from cluster ARN
		region := r.extractRegionFromArn(r.clusterArns[i])
		regionGroups[region] = append(regionGroups[region], clusterMetrics)
	}

	return regionGroups
}

func (r *MetricReporter) extractRegionFromArn(arn string) string {
	// ARN format: arn:aws:kafka:region:account:cluster/cluster-name/uuid
	// Split on ':' and take index 3 for region
	parts := strings.Split(arn, ":")
	if len(parts) >= 4 {
		return parts[3]
	}
	return "unknown-region"
}

func (r *MetricReporter) addRegionSection(md *markdown.Markdown, region string, clusters []types.ProcessedClusterMetrics) {
	md.AddHeading(fmt.Sprintf("Region: %s", region), 2)

	// Process each cluster in this region
	for i, clusterMetrics := range clusters {
		if i > 0 {
			md.AddParagraph("---")
		}
		r.addClusterSection(md, clusterMetrics, region)
	}

	md.AddHorizontalRule()
}

func (r *MetricReporter) addClusterSection(md *markdown.Markdown, clusterMetrics types.ProcessedClusterMetrics, region string) {
	// Extract cluster name from ARN - we need to find the matching ARN for this region
	clusterName := r.extractClusterNameFromRegion(region)

	md.AddHeading(fmt.Sprintf("Cluster: %s", clusterName), 3)

	// Add metric aggregates
	if len(clusterMetrics.Aggregates) > 0 {
		md.AddHeading("Metric Aggregates", 4)

		headers := []string{"Metric", "Average", "Maximum", "Minimum"}
		var tableData [][]string

		for metricName, aggregate := range clusterMetrics.Aggregates {
			row := []string{
				metricName,
				r.formatMetricValue(aggregate.Average),
				r.formatMetricValue(aggregate.Maximum),
				r.formatMetricValue(aggregate.Minimum),
			}
			tableData = append(tableData, row)
		}

		if len(tableData) > 0 {
			md.AddTable(headers, tableData)
		}
	} else {
		md.AddParagraph("*No metric aggregates available for this cluster.*")
	}
}

func (r *MetricReporter) extractClusterNameFromRegion(region string) string {
	// Find the first ARN that matches this region
	for _, arn := range r.clusterArns {
		if r.extractRegionFromArn(arn) == region {
			return r.extractClusterNameFromArn(arn)
		}
	}
	return "Unknown"
}

func (r *MetricReporter) extractClusterNameFromArn(arn string) string {
	// ARN format: arn:aws:kafka:region:account:cluster/cluster-name/uuid
	// Split on '/' and take index 1 for cluster name
	parts := strings.Split(arn, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return "unknown-cluster"
}

func (r *MetricReporter) formatMetricValue(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f", *value)
}
