package costs

import (
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/types"
)

func (rc *RegionCoster) writeCostReportMarkdown(metrics types.RegionCosts, outputFolder string) error {
	md := markdown.New()
	md.AddHeading(fmt.Sprintf("AWS MSK Cost Report - %s", rc.region), 1)
	md.AddParagraph(fmt.Sprintf("**Report Period:** %s to %s", rc.startDate.Format("2006-01-02"), rc.endDate.Format("2006-01-02")))
	md.AddParagraph(fmt.Sprintf("**Granularity:** %s", rc.granularity))
	md.AddParagraph(fmt.Sprintf("**Total Cost:** $%.2f USD", metrics.CostData.Total))

	// Usage Type Summary
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

	md.AddHeading("Usage Type Summary", 2)
	usageHeaders := []string{"Service", "Usage Type", "Total Cost (USD)"}
	usageData := [][]string{}

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
			usageData = append(usageData, []string{service, usageType, fmt.Sprintf("$%.2f", totalCost)})
		}
		// Add service total row
		usageData = append(usageData, []string{service, "**TOTAL**", fmt.Sprintf("$%.2f", serviceTotals[service])})
		// Add empty row for spacing
		usageData = append(usageData, []string{"", "", ""})
	}

	// Add grand total at the bottom
	usageData = append(usageData, []string{"**GRAND TOTAL**", "", fmt.Sprintf("$%.2f", grandTotal)})

	md.AddTable(usageHeaders, usageData, 0, 1)

	// Detailed Cost Breakdown
	md.AddHeading("Detailed Cost Breakdown", 2)
	headers := []string{"Time Period Start", "Time Period End", "Service", "Usage Type", "Cost (USD)"}
	data := [][]string{}

	// Build data rows directly from the costs
	for _, cost := range metrics.CostData.Costs {
		startTime := cost.TimePeriodStart
		endTime := cost.TimePeriodEnd

		// Format the time strings
		if start, err := time.Parse("2006-01-02T15:04:05Z", startTime); err == nil {
			startTime = start.Format("2006-01-02 15:04")
		} else if start, err := time.Parse("2006-01-02", startTime); err == nil {
			startTime = start.Format("2006-01-02")
		}

		if end, err := time.Parse("2006-01-02T15:04:05Z", endTime); err == nil {
			endTime = end.Format("2006-01-02 15:04")
		} else if end, err := time.Parse("2006-01-02", endTime); err == nil {
			endTime = end.Format("2006-01-02")
		}

		data = append(data, []string{
			startTime,
			endTime,
			cost.Service,
			cost.UsageType,
			fmt.Sprintf("$%.2f", cost.Cost),
		})
	}

	// Use skip repeat for time period columns (0 and 1) to avoid repeating start/end times
	md.AddTable(headers, data, 0, 1, 2)

	// Print to terminal and save to file
	filePath := fmt.Sprintf("%s/cost_report-%s.md", outputFolder, rc.region)
	err := md.Print(markdown.PrintOptions{ToTerminal: true, ToFile: filePath})
	if err != nil {
		return fmt.Errorf("❌ Failed to write markdown output: %v", err)
	}
	return nil
}
