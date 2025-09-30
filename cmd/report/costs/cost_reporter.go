package costs

import (
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState
	FilterRegionCosts(processedState types.ProcessedState, regionName string, options ...report.FilterRegionCostsOption) (*types.ProcessedRegionCosts, error)
}

type CostReporterOpts struct {
	Regions   []string
	State     *types.State
	StartDate time.Time
	EndDate   time.Time
	CostType  string
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
	costType  string
}

func NewCostReporter(reportService ReportService, markdownService markdown.Markdown, opts CostReporterOpts) *CostReporter {
	return &CostReporter{
		reportService:   reportService,
		markdownService: markdownService,
		regions:         opts.Regions,
		state:           opts.State,
		startDate:       opts.StartDate,
		endDate:         opts.EndDate,
		costType:        opts.CostType,
	}
}

func (r *CostReporter) Run() error {
	processedState := r.reportService.ProcessState(*r.state)
	regionCostData := []RegionCostData{}

	for _, region := range r.regions {
		regionCosts, err := r.reportService.FilterRegionCosts(processedState, region,
			report.WithStartTime(r.startDate),
			report.WithEndTime(r.endDate),
			report.WithCostType(r.costType),
		)
		if err != nil {
			return fmt.Errorf("failed to filter region costs: %v", err)
		}
		regionCostData = append(regionCostData, RegionCostData{
			RegionName: region,
			Costs:      *regionCosts,
		})
	}

	if err := r.generateReport(regionCostData).Print(markdown.PrintOptions{
		ToTerminal: false,
		ToFile:     "report.md",
	}); err != nil {
		return fmt.Errorf("failed to write markdown report: %v", err)
	}

	return nil
}

func (r *CostReporter) generateReport(regionCostData []RegionCostData) *markdown.Markdown {
	md := markdown.New()

	// Add main report header
	md.AddHeading("AWS Cost Report", 1)

	if len(regionCostData) > 0 {
		metadata := regionCostData[0].Costs.Metadata
		md.AddParagraph(fmt.Sprintf("**Report Period:** %s to %s",
			metadata.StartDate.Format("2006-01-02"),
			metadata.EndDate.Format("2006-01-02")))
		md.AddParagraph(fmt.Sprintf("**Granularity:** %s", metadata.Granularity))

		if len(metadata.Services) > 0 {
			md.AddParagraph(fmt.Sprintf("**Services:** %s", fmt.Sprintf("%v", metadata.Services)))
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

		r.addRegionSection(md, regionData.RegionName, regionData.Costs)
	}

	return md
}

func (r *CostReporter) addRegionSection(md *markdown.Markdown, regionName string, regionCosts types.ProcessedRegionCosts) {
	md.AddHeading(fmt.Sprintf("Region: %s", regionName), 2)

	// Add metadata details for this region
	metadata := regionCosts.Metadata
	md.AddParagraph(fmt.Sprintf("**Period:** %s to %s | **Granularity:** %s",
		metadata.StartDate.Format("2006-01-02"),
		metadata.EndDate.Format("2006-01-02"),
		metadata.Granularity))

	// Add aggregate cost summaries for each service
	r.addServiceAggregates(md, "AWS Certificate Manager", regionCosts.Aggregates.AWSCertificateManager)
	r.addServiceAggregates(md, "Amazon Managed Streaming for Apache Kafka", regionCosts.Aggregates.AmazonManagedStreamingForApacheKafka)
	r.addServiceAggregates(md, "EC2 - Other", regionCosts.Aggregates.EC2Other)
}

func (r *CostReporter) addServiceAggregates(md *markdown.Markdown, serviceName string, aggregates types.ServiceCostAggregates) {
	// Check if this service has any cost data
	hasData := r.hasServiceData(aggregates)
	if !hasData {
		return
	}

	md.AddHeading(serviceName, 3)

	// Create table for cost metrics
	headers := []string{"Cost Type", "Usage Type", "Sum ($)", "Average ($)", "Min ($)", "Max ($)"}
	var tableData [][]string

	// Process each cost type
	costTypes := map[string]map[string]any{
		"Unblended Cost":     aggregates.UnblendedCost,
		"Blended Cost":       aggregates.BlendedCost,
		"Amortized Cost":     aggregates.AmortizedCost,
		"Net Amortized Cost": aggregates.NetAmortizedCost,
		"Net Unblended Cost": aggregates.NetUnblendedCost,
	}

	for costType, usageTypes := range costTypes {
		if len(usageTypes) == 0 {
			continue
		}

		for usageType, aggregateData := range usageTypes {
			if costAggregate, ok := aggregateData.(types.CostAggregate); ok {
				row := []string{
					costType,
					usageType,
					r.formatCurrency(costAggregate.Sum),
					r.formatCurrency(costAggregate.Average),
					r.formatCurrency(costAggregate.Minimum),
					r.formatCurrency(costAggregate.Maximum),
				}
				tableData = append(tableData, row)
			}
		}
	}

	if len(tableData) > 0 {
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

func (r *CostReporter) formatCurrency(value *float64) string {
	if value == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f", *value)
}
