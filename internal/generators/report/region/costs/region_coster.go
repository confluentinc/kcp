package costs

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/confluentinc/kcp-internal/internal/types"
)

type RegionCosterOpts struct {
	Region      string
	StartDate   time.Time
	EndDate     time.Time
	Granularity costexplorertypes.Granularity
	Tag         []string
}

type RegionCoster struct {
	region      string
	costService CostService
	startDate   time.Time
	endDate     time.Time
	granularity costexplorertypes.Granularity
	tags        []string
}

type CostService interface {
	GetMonthlyCosts(region string, tags map[string][]string) (types.CostData, error)
	GetCostsForTimeRange(region string, startDate time.Time, endDate time.Time, granularity costexplorertypes.Granularity, tags map[string][]string) (types.CostData, error)
}

func NewRegionCoster(costService CostService, opts RegionCosterOpts) *RegionCoster {
	return &RegionCoster{
		region:      opts.Region,
		costService: costService,
		startDate:   opts.StartDate,
		endDate:     opts.EndDate,
		granularity: opts.Granularity,
		tags:        opts.Tag,
	}
}

func (rc *RegionCoster) convertTagsToMap() map[string][]string {
	if len(rc.tags) == 0 {
		return nil
	}

	tagMap := make(map[string][]string)
	for _, tag := range rc.tags {
		parts := strings.Split(tag, "=")
		if len(parts) == 2 {
			key := parts[0]
			value := parts[1]
			tagMap[key] = append(tagMap[key], value)
		}
	}
	return tagMap
}

func (rc *RegionCoster) Run() error {
	slog.Info("🚀 starting region costs report", "region", rc.region)

	tags := rc.convertTagsToMap()
	costData, err := rc.costService.GetCostsForTimeRange(rc.region, rc.startDate, rc.endDate, rc.granularity, tags)
	if err != nil {
		return fmt.Errorf("❌ Failed to get AWS costs: %v", err)
	}

	regionCosts := types.RegionCosts{
		Region:   rc.region,
		CostData: costData,
	}

	outputFolder := fmt.Sprintf("cost_reports/%s", rc.region)
	if err := os.MkdirAll(outputFolder, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create output folder: %v", err)
	}

	if err := rc.writeCostReportJSON(regionCosts, outputFolder); err != nil {
		return fmt.Errorf("❌ Failed to write JSON output: %v", err)
	}

	if err := rc.writeCostReportMarkdown(regionCosts, outputFolder); err != nil {
		return fmt.Errorf("❌ Failed to write markdown output: %v", err)
	}

	if err := rc.writeCostReportCSV(regionCosts, outputFolder); err != nil {
		return fmt.Errorf("❌ Failed to write CSV output: %v", err)
	}

	slog.Info("✅ region costs report complete", "region", rc.region)
	return nil
}

func (rc *RegionCoster) writeCostReportJSON(metrics types.RegionCosts, outputFolder string) error {
	data, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cluster information: %v", err)
	}

	filePath := fmt.Sprintf("%s/cost_report-%s.json", outputFolder, rc.region)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	return nil
}

func (rc *RegionCoster) writeCostReportCSV(metrics types.RegionCosts, outputFolder string) error {
	filePath := fmt.Sprintf("%s/cost_report-%s.csv", outputFolder, rc.region)

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write summary section
	writer.Write([]string{"SUMMARY"})
	writer.Write([]string{"Service", "Usage Type", "Total Cost (USD)"})

	// Calculate summary data
	usageTypeSummary := make(map[string]map[string]float64) // service -> usageType -> cost
	serviceTotals := make(map[string]float64)               // service -> total cost
	var grandTotal float64

	for _, cost := range metrics.CostData.Costs {
		if usageTypeSummary[cost.Service] == nil {
			usageTypeSummary[cost.Service] = make(map[string]float64)
		}
		usageTypeSummary[cost.Service][cost.UsageType] += cost.Cost
		serviceTotals[cost.Service] += cost.Cost
		grandTotal += cost.Cost
	}

	// Sort services for consistent output
	services := make([]string, 0, len(usageTypeSummary))
	for service := range usageTypeSummary {
		services = append(services, service)
	}
	// Sort services alphabetically
	for i := 0; i < len(services)-1; i++ {
		for j := i + 1; j < len(services); j++ {
			if services[i] > services[j] {
				services[i], services[j] = services[j], services[i]
			}
		}
	}

	for _, service := range services {
		usageTypes := make([]string, 0, len(usageTypeSummary[service]))
		for usageType := range usageTypeSummary[service] {
			usageTypes = append(usageTypes, usageType)
		}
		// Sort usage types alphabetically
		for i := 0; i < len(usageTypes)-1; i++ {
			for j := i + 1; j < len(usageTypes); j++ {
				if usageTypes[i] > usageTypes[j] {
					usageTypes[i], usageTypes[j] = usageTypes[j], usageTypes[i]
				}
			}
		}

		for _, usageType := range usageTypes {
			totalCost := usageTypeSummary[service][usageType]
			writer.Write([]string{service, usageType, fmt.Sprintf("%.2f", totalCost)})
		}
		// Add service total row
		writer.Write([]string{service, "TOTAL", fmt.Sprintf("%.2f", serviceTotals[service])})
		// Add empty row for spacing
		writer.Write([]string{"", "", ""})
	}

	// Add grand total at the bottom
	writer.Write([]string{"GRAND TOTAL", "", fmt.Sprintf("%.2f", grandTotal)})

	// Add empty row between summary and detailed breakdown
	writer.Write([]string{""})
	writer.Write([]string{""})

	// Write detailed breakdown section
	writer.Write([]string{"DETAILED BREAKDOWN"})
	header := []string{"Time Period Start", "Time Period End", "Service", "Usage Type", "Cost (USD)"}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %v", err)
	}

	// Write cost data
	for _, cost := range metrics.CostData.Costs {
		record := []string{
			cost.TimePeriodStart,
			cost.TimePeriodEnd,
			cost.Service,
			cost.UsageType,
			fmt.Sprintf("%.2f", cost.Cost),
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %v", err)
		}
	}

	return nil
}
