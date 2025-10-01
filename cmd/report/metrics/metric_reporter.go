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
	FilterClusterMetrics(processedState types.ProcessedState, regionName, clusterArn string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error)
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

	for _, clusterArn := range r.clusterArns {
		clusterMetrics, err := r.reportService.FilterClusterMetrics(processedState, r.region, clusterArn, &r.startDate, &r.endDate)
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

	md.AddHeading("AWS Metric Report", 1)

	return md
}
