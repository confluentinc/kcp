package costs

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState
	FilterRegionCosts(processedState types.ProcessedState, regionName string, startTime, endTime *time.Time) (*types.ProcessedRegionCosts, error)
}

type CostReporterOpts struct {
	Regions   []string
	State     *types.State
	StartDate time.Time
	EndDate   time.Time
}

type RegionCostData struct {
	RegionName string
	Costs      types.ProcessedRegionCosts
}

type CostReporter struct {
	reportService   ReportService
	markdownService markdown.Markdown

	regions   []string
	state     *types.State
	startDate time.Time
	endDate   time.Time
}

func NewCostReporter(reportService ReportService, markdownService markdown.Markdown, opts CostReporterOpts) *CostReporter {
	return &CostReporter{
		reportService:   reportService,
		markdownService: markdownService,
		regions:         opts.Regions,
		state:           opts.State,
		startDate:       opts.StartDate,
		endDate:         opts.EndDate,
	}
}

func (r *CostReporter) Run() error {
	slog.Info("🔍 processing regions", "regions", r.regions, "startDate", r.startDate, "endDate", r.endDate)

	processedState := r.reportService.ProcessState(*r.state)
	regionCostData := []types.ProcessedRegionCosts{}

	for _, region := range r.regions {
		regionCosts, err := r.reportService.FilterRegionCosts(processedState, region, &r.startDate, &r.endDate)
		if err != nil {
			return fmt.Errorf("failed to filter region costs: %v", err)
		}

		regionCostData = append(regionCostData, *regionCosts)
	}

	fileName := fmt.Sprintf("cost_report_%s.md", time.Now().Format("2006-01-02_15-04-05"))
	markdownReport := r.generateReport(regionCostData)
	if err := markdownReport.Print(markdown.PrintOptions{ToTerminal: false, ToFile: fileName}); err != nil {
		return fmt.Errorf("failed to write markdown report: %v", err)
	}

	return nil
}

func (r *CostReporter) generateReport(regionCostData []types.ProcessedRegionCosts) *markdown.Markdown {
	md := markdown.New()

	// Add main report header
	md.AddHeading("AWS Cost Report", 1)

	// Use the actual date range from the reporter parameters
	md.AddParagraph(fmt.Sprintf("**Report Period:** %s to %s",
		r.startDate.Format("2006-01-02"),
		r.endDate.Format("2006-01-02")))

	if len(regionCostData) > 0 {
		metadata := regionCostData[0].Metadata
		md.AddParagraph(fmt.Sprintf("**Granularity:** %s", metadata.Granularity))

		if len(metadata.Services) > 0 {
			md.AddParagraph("**Services:**")
			md.AddList(metadata.Services)
		}

		if len(metadata.Tags) > 0 {
			md.AddParagraph(fmt.Sprintf("**Tags:** %v", metadata.Tags))
		}
	}

	md.AddHorizontalRule()

	// Process each region
	for i, regionData := range regionCostData {
		if i > 0 {
			md.AddHorizontalRule()
		}

		r.addRegionSection(md, regionData.Region, regionData)
	}

	return md
}

func (r *CostReporter) addRegionSection(md *markdown.Markdown, regionName string, regionCosts types.ProcessedRegionCosts) {
	md.AddHeading(fmt.Sprintf("Region: %s", regionName), 2)

	// Add aggregate cost summaries for each service
	r.addServiceAggregates(md, "Amazon Managed Streaming for Apache Kafka", regionCosts.Aggregates.AmazonManagedStreamingForApacheKafka)

	r.addServiceAggregates(md, "EC2 - Other", regionCosts.Aggregates.EC2Other)

	r.addServiceAggregates(md, "AWS Certificate Manager", regionCosts.Aggregates.AWSCertificateManager)
}

func (r *CostReporter) addServiceAggregates(md *markdown.Markdown, serviceName string, aggregates types.ServiceCostAggregates) {
	md.AddHeading(fmt.Sprintf("▪ %s", serviceName), 3)

	// Check if this service has any cost data
	hasData := r.hasServiceData(aggregates)
	if !hasData {
		md.AddParagraph("*No costs recorded for this service in the specified time period.*")
		md.AddParagraph("---")
		return
	}

	// Create separate tables for each cost type
	costTypes := []struct {
		name string
		data map[string]any
	}{
		{"Unblended Cost", aggregates.UnblendedCost},
		{"Blended Cost", aggregates.BlendedCost},
		{"Amortized Cost", aggregates.AmortizedCost},
		{"Net Amortized Cost", aggregates.NetAmortizedCost},
		{"Net Unblended Cost", aggregates.NetUnblendedCost},
	}

	for _, costType := range costTypes {
		if len(costType.data) == 0 {
			continue
		}

		md.AddHeading(costType.name, 4)

		headers := []string{"Usage Type", "Sum ($)", "Average ($)", "Min ($)", "Max ($)"}
		var tableData [][]string

		for usageType, aggregateData := range costType.data {
			if costAggregate, ok := aggregateData.(types.CostAggregate); ok {
				row := []string{
					usageType,
					r.formatCurrency(costAggregate.Sum),
					r.formatCurrency(costAggregate.Average),
					r.formatCurrency(costAggregate.Minimum),
					r.formatCurrency(costAggregate.Maximum),
				}
				tableData = append(tableData, row)
			}
		}

		if len(tableData) > 0 {
			md.AddTable(headers, tableData)
		}
	}

	// Add separator after service section
	md.AddParagraph("---")
}

func (r *CostReporter) hasServiceData(aggregates types.ServiceCostAggregates) bool {
	return len(aggregates.UnblendedCost) > 0 ||
		len(aggregates.BlendedCost) > 0 ||
		len(aggregates.AmortizedCost) > 0 ||
		len(aggregates.NetAmortizedCost) > 0 ||
		len(aggregates.NetUnblendedCost) > 0
}

func (r *CostReporter) formatCurrency(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f", *value)
}
