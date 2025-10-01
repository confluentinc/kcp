package metrics

import (
	"fmt"
	"log/slog"
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
	Region      string
}

type MetricReporter struct {
	reportService ReportService

	clusterArns []string
	state       *types.State
	startDate   time.Time
	endDate     time.Time
	region      string
}

func NewMetricReporter(reportService ReportService, opts MetricReporterOpts) *MetricReporter {
	return &MetricReporter{
		reportService: reportService,

		clusterArns: opts.ClusterArns,
		state:       opts.State,
		startDate:   opts.StartDate,
		endDate:     opts.EndDate,
		region:      opts.Region,
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

	// Add main report header
	md.AddHeading("AWS Metrics Report", 1)

	// Use the actual date range from the reporter parameters
	md.AddParagraph(fmt.Sprintf("**Report Period:** %s to %s",
		r.startDate.Format("2006-01-02"),
		r.endDate.Format("2006-01-02")))

	md.AddParagraph(fmt.Sprintf("**Region:** %s", r.region))

	if len(processedClusterMetrics) > 0 {
		metadata := processedClusterMetrics[0].Metadata
		md.AddParagraph(fmt.Sprintf("**Cluster Type:** %s", metadata.ClusterType))
		md.AddParagraph(fmt.Sprintf("**Kafka Version:** %s", metadata.KafkaVersion))
		md.AddParagraph(fmt.Sprintf("**Enhanced Monitoring:** %s", metadata.EnhancedMonitoring))
		md.AddParagraph(fmt.Sprintf("**Period:** %d seconds", metadata.Period))
	}

	md.AddHorizontalRule()

	// Process each cluster
	for i, clusterMetrics := range processedClusterMetrics {
		if i > 0 {
			md.AddHorizontalRule()
		}

		r.addClusterSection(md, clusterMetrics)
	}

	return md
}

func (r *MetricReporter) addClusterSection(md *markdown.Markdown, clusterMetrics types.ProcessedClusterMetrics) {
	// Find cluster name from the ARN (last part after the last slash)
	clusterName := "Unknown"
	for _, arn := range r.clusterArns {
		// Simple way to extract cluster name from ARN
		if len(arn) > 0 {
			parts := []rune(arn)
			for i := len(parts) - 1; i >= 0; i-- {
				if parts[i] == '/' {
					clusterName = string(parts[i+1:])
					break
				}
			}
			break // Use first cluster for now, we'll improve this
		}
	}

	md.AddHeading(fmt.Sprintf("Cluster: %s", clusterName), 2)

	// Add metric aggregates
	if len(clusterMetrics.Aggregates) > 0 {
		md.AddHeading("Metric Aggregates", 3)

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

	// Add separator after cluster section
	md.AddParagraph("---")
}

func (r *MetricReporter) formatMetricValue(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f", *value)
}
