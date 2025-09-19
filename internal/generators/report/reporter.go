package report

import (
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
	r.generateReport(r.Discovery)
	return nil
}

func (r *Reporter) generateReport(discovery types.Discovery) error {
	parsedRegionsCost := make([]types.ParsedRegionCostResponse, 0, len(discovery.Regions))
	for _, region := range discovery.Regions {
		parsedRegionCost := r.ReportService.ParseCostResults(region.Name, region.Costs)
		parsedRegionsCost = append(parsedRegionsCost, parsedRegionCost)
	}

	parsedCostWrapper := types.CostReport{
		ParsedRegionCosts: parsedRegionsCost,
	}

	costMarkdown := parsedCostWrapper.AsMarkdown()
	costMarkdown.Print(markdown.PrintOptions{ToTerminal: true, ToFile: "report.md"})

	return nil
}
