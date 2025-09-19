package report

import (
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

type ReporterOpts struct {
	Discovery types.Discovery
}

type Reporter struct {
	ReportService   report.ReportService
	MarkdownService markdown.Markdown
	Discovery       types.Discovery
}

func NewReporter(reportService report.ReportService, markdownService markdown.Markdown, opts ReporterOpts) *Reporter {
	return &Reporter{
		ReportService:   reportService,
		MarkdownService: markdownService,
		Discovery:       opts.Discovery,
	}
}

func (r *Reporter) Run() error {
	slog.Info("🔍 running reporter")
	if err := r.generateReport(r.Discovery); err != nil {
		return fmt.Errorf("failed to generate report: %v", err)
	}

	return nil
}

func (r *Reporter) generateReport(discovery types.Discovery) error {
	processedRegionsCosts := []types.ProcessedRegionCosts{}
	for _, region := range discovery.Regions {
		parsedRegionCost := r.ReportService.ProcessCosts(region)
		processedRegionsCosts = append(processedRegionsCosts, parsedRegionCost)
	}

	// do stuff with metrics now
	allClusterMetrics := []types.ProcessedClusterMetrics{}
	for _, region := range discovery.Regions {
		for _, cluster := range region.Clusters {
			clusterMetrics := r.ReportService.ProcessMetrics(cluster)
			allClusterMetrics = append(allClusterMetrics, clusterMetrics)
		}
	}


	// prob want one report with both costs per region and metrics per cluster in the region
	metricsReport := types.MetricsReport{
		ProcessedClusterMetrics: allClusterMetrics,
	}

	metricsMarkdown := metricsReport.AsMarkdown()
	metricsMarkdown.Print(markdown.PrintOptions{ToTerminal: true, ToFile: "metrics_report.md"})

	costReport := types.CostReport{
		ProcessedRegionCosts: processedRegionsCosts,
	}

	costMarkdown := costReport.AsMarkdown()
	costMarkdown.Print(markdown.PrintOptions{ToTerminal: true, ToFile: "cost_report.md"})

	return nil
}
