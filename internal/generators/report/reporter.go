package report

import (
	"fmt"
	"log/slog"
	"os"

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

	parsedCostWrapper := types.ParseCostWrapper{
		ParsedRegionCosts: parsedRegionsCost,
	}

	markdown := parsedCostWrapper.AsMarkdown()
	// write to file
	filePath := fmt.Sprintf("report-%s.md", discovery.Timestamp.Format("2006-01-02"))
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()
	markdown.WriteTo(file)

	return nil
}
