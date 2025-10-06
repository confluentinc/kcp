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
	StartDate *time.Time
	EndDate   *time.Time
}

type CostReporter struct {
	reportService   ReportService
	markdownService markdown.Markdown

	regions   []string
	state     *types.State
	startDate *time.Time
	endDate   *time.Time
}

func NewCostReporter(reportService ReportService, markdownService markdown.Markdown, opts CostReporterOpts) *CostReporter {
	return &CostReporter{
		reportService:   reportService,
		markdownService: markdownService,

		regions:   opts.Regions,
		state:     opts.State,
		startDate: opts.StartDate,
		endDate:   opts.EndDate,
	}
}

func (r *CostReporter) Run() error {
	slog.Info("🔍 processing regions", "regions", r.regions, "startDate", r.startDate, "endDate", r.endDate)

	processedState := r.reportService.ProcessState(*r.state)
	regionCostData := []types.ProcessedRegionCosts{}

	for _, region := range r.regions {
		regionCosts, err := r.reportService.FilterRegionCosts(processedState, region, r.startDate, r.endDate)
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

	// Add cost summary section
	r.addCostSummary(md, regionCostData)

	// Process each region
	for _, regionData := range regionCostData {
		r.addRegionSection(md, regionData.Region, regionData)
	}

	return md
}

func (r *CostReporter) addRegionSection(md *markdown.Markdown, regionName string, regionCosts types.ProcessedRegionCosts) {
	md.AddHeading(fmt.Sprintf("Region: %s", regionName), 2)
	md.AddParagraph(fmt.Sprintf("*Detailed cost breakdown for %s region*", regionName))
	md.AddParagraph("")

	// Add aggregate cost summaries for each service
	r.addServiceAggregates(md, "Amazon Managed Streaming for Apache Kafka", regionCosts.Aggregates.AmazonManagedStreamingForApacheKafka)

	r.addServiceAggregates(md, "EC2 - Other", regionCosts.Aggregates.EC2Other)

	r.addServiceAggregates(md, "AWS Certificate Manager", regionCosts.Aggregates.AWSCertificateManager)

	md.AddParagraph("")
	md.AddParagraph("---")
	md.AddParagraph("")
}

func (r *CostReporter) addServiceAggregates(md *markdown.Markdown, serviceName string, aggregates types.ServiceCostAggregates) {
	md.AddHeading(fmt.Sprintf("▪ %s", serviceName), 3)

	// Check if this service has any cost data
	hasData := r.hasServiceData(aggregates)
	if !hasData {
		md.AddParagraph("*No costs recorded for this service in the specified time period.*")
		md.AddParagraph("")
		return
	}

	// Create a single comprehensive table with all cost types
	r.addServiceCostTable(md, aggregates)
	md.AddParagraph("")
}

func (r *CostReporter) addServiceCostTable(md *markdown.Markdown, aggregates types.ServiceCostAggregates) {
	// Collect all unique usage types across all cost types
	usageTypes := make(map[string]bool)

	costTypeMaps := []map[string]any{
		aggregates.UnblendedCost,
		aggregates.BlendedCost,
		aggregates.AmortizedCost,
		aggregates.NetAmortizedCost,
		aggregates.NetUnblendedCost,
	}

	for _, costMap := range costTypeMaps {
		for usageType := range costMap {
			// Skip the "total" key as it's a service-level aggregate, not a usage type
			if usageType != "total" {
				usageTypes[usageType] = true
			}
		}
	}

	if len(usageTypes) == 0 {
		return
	}

	// Create table headers
	headers := []string{"Usage Type", "Unblended ($)", "Blended ($)", "Amortized ($)", "Net Amortized ($)", "Net Unblended ($)"}
	var tableData [][]string

	// Create rows for each usage type
	for usageType := range usageTypes {
		row := []string{usageType}

		// Add cost values for each cost type
		costTypes := []map[string]any{
			aggregates.UnblendedCost,
			aggregates.BlendedCost,
			aggregates.AmortizedCost,
			aggregates.NetAmortizedCost,
			aggregates.NetUnblendedCost,
		}

		for _, costMap := range costTypes {
			if aggregateData, exists := costMap[usageType]; exists {
				if costAggregate, ok := aggregateData.(types.CostAggregate); ok {
					value := 0.0
					if costAggregate.Sum != nil {
						value = *costAggregate.Sum
					}
					row = append(row, r.formatCurrency(&value))
				} else {
					row = append(row, "0.00")
				}
			} else {
				row = append(row, "0.00")
			}
		}

		tableData = append(tableData, row)
	}

	// Add total row using the backend-calculated totals
	if len(tableData) > 0 {
		totalRow := []string{"**Total**"}

		// Use the "total" values directly from the backend
		costTypes := []map[string]any{
			aggregates.UnblendedCost,
			aggregates.BlendedCost,
			aggregates.AmortizedCost,
			aggregates.NetAmortizedCost,
			aggregates.NetUnblendedCost,
		}

		for _, costMap := range costTypes {
			if totalData, exists := costMap["total"]; exists {
				// The "total" key contains a float64, not a CostAggregate
				if total, ok := totalData.(float64); ok {
					totalRow = append(totalRow, fmt.Sprintf("**%.2f**", total))
				} else {
					totalRow = append(totalRow, "**0.00**")
				}
			} else {
				totalRow = append(totalRow, "**0.00**")
			}
		}

		tableData = append(tableData, totalRow)
		md.AddTable(headers, tableData)
	}
}

func (r *CostReporter) hasServiceData(aggregates types.ServiceCostAggregates) bool {
	return len(aggregates.UnblendedCost) > 0 ||
		len(aggregates.BlendedCost) > 0 ||
		len(aggregates.AmortizedCost) > 0 ||
		len(aggregates.NetAmortizedCost) > 0 ||
		len(aggregates.NetUnblendedCost) > 0
}

func (r *CostReporter) addCostSummary(md *markdown.Markdown, regionCostData []types.ProcessedRegionCosts) {
	md.AddHeading("Cost Summary", 2)
	md.AddParagraph("*Overview of total costs across all regions and cost types*")
	md.AddParagraph("")

	// Calculate totals for each cost type
	costTypeNames := []string{"Unblended", "Blended", "Amortized", "Net Amortized", "Net Unblended"}
	overallTotals := make([]float64, len(costTypeNames))
	var summaryData [][]string

	for _, regionData := range regionCostData {
		regionTotals := r.calculateRegionTotalsAllTypes(regionData)

		row := []string{regionData.Region}
		for i, total := range regionTotals {
			row = append(row, r.formatCurrency(&total))
			overallTotals[i] += total
		}
		summaryData = append(summaryData, row)
	}

	// Add overall totals row
	overallRow := []string{"**Overall Total**"}
	for _, total := range overallTotals {
		overallRow = append(overallRow, fmt.Sprintf("**%.2f**", total))
	}
	summaryData = append(summaryData, overallRow)

	// Create headers
	headers := []string{"Region", "Unblended ($)", "Blended ($)", "Amortized ($)", "Net Amortized ($)", "Net Unblended ($)"}
	md.AddTable(headers, summaryData)
	md.AddParagraph("")
	md.AddParagraph("---")
	md.AddParagraph("")
}

func (r *CostReporter) calculateRegionTotalsAllTypes(regionData types.ProcessedRegionCosts) []float64 {
	// Return totals for: Unblended, Blended, Amortized, Net Amortized, Net Unblended
	totals := make([]float64, 5)

	services := []types.ServiceCostAggregates{
		regionData.Aggregates.AmazonManagedStreamingForApacheKafka,
		regionData.Aggregates.EC2Other,
		regionData.Aggregates.AWSCertificateManager,
	}

	for _, service := range services {
		costMaps := []map[string]any{
			service.UnblendedCost,
			service.BlendedCost,
			service.AmortizedCost,
			service.NetAmortizedCost,
			service.NetUnblendedCost,
		}

		for i, costMap := range costMaps {
			for _, aggregateData := range costMap {
				if costAggregate, ok := aggregateData.(types.CostAggregate); ok {
					if costAggregate.Sum != nil {
						totals[i] += *costAggregate.Sum
					}
				}
			}
		}
	}

	return totals
}

func (r *CostReporter) formatCurrency(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f", *value)
}
