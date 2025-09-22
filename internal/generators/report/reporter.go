package report

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

type ReporterOpts struct {
	State types.State
}

type Reporter struct {
	ReportService   report.ReportService
	MarkdownService markdown.Markdown
	State           types.State
}

func NewReporter(reportService report.ReportService, markdownService markdown.Markdown, opts ReporterOpts) *Reporter {
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
	processedRegionsCosts := []types.ProcessedRegionCosts{}
	for _, region := range state.Regions {
		parsedRegionCost := r.ReportService.ProcessCosts(region)
		processedRegionsCosts = append(processedRegionsCosts, parsedRegionCost)
	}

	// do stuff with metrics now
	allClusterMetrics := []types.ProcessedClusterMetrics{}
	for _, region := range state.Regions {
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

	report := types.Report{
		Costs:   processedRegionsCosts,
		Metrics: allClusterMetrics,
	}

	// outputting whole thing for testing
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %v", err)
	}

	if err := os.WriteFile("report.json", data, 0644); err != nil {
		return fmt.Errorf("failed to write report: %v", err)
	}

	return nil
}
