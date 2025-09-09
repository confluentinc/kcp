package types

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/markdown"
)

type Cost struct {
	Timestamp       time.Time `json:"timestamp"`
	TimePeriodStart string    `json:"time_period_start"`
	TimePeriodEnd   string    `json:"time_period_end"`
	Service         string    `json:"service"`
	Cost            float64   `json:"cost"`
	UsageType       string    `json:"usage_type"`
}

type CostData struct {
	Costs []Cost  `json:"costs"`
	Total float64 `json:"total"`
}

type RegionCosts struct {
	KcpBuildInfo KcpBuildInfo        `json:"kcp_build_info"`
	Timestamp    time.Time           `json:"timestamp"`
	Region       string              `json:"region"`
	CostData     CostData            `json:"cost_data"`
	StartDate    time.Time           `json:"start_date"`
	EndDate      time.Time           `json:"end_date"`
	Granularity  string              `json:"granularity"`
	Tags         map[string][]string `json:"tags"`
}

func NewRegionCosts(region string, timestamp time.Time) *RegionCosts {
	return &RegionCosts{
		Region:    region,
		Timestamp: timestamp,
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
	}
}

func (c *RegionCosts) GetJsonPath() string {
	return filepath.Join(c.GetDirPath(), fmt.Sprintf("%s-cost-report.json", c.Region))
}

func (c *RegionCosts) GetMarkdownPath() string {
	return filepath.Join(c.GetDirPath(), fmt.Sprintf("%s-cost-report.md", c.Region))
}

func (c *RegionCosts) GetCSVPath() string {
	return filepath.Join(c.GetDirPath(), fmt.Sprintf("%s-cost-report.csv", c.Region))
}

func (c *RegionCosts) GetDirPath() string {
	return filepath.Join("kcp-scan", c.Region)
}

func (c *RegionCosts) WriteAsJson() error {

	dirPath := c.GetDirPath()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := c.GetJsonPath()

	data, err := c.AsJson()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("❌ Failed to write file: %v", err)
	}

	return nil
}

func (c *RegionCosts) AsJson() ([]byte, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to marshal scan results: %v", err)
	}
	return data, nil
}

func (c *RegionCosts) WriteAsMarkdown(suppressToTerminal bool) error {
	dirPath := c.GetDirPath()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := c.GetMarkdownPath()
	md := c.AsMarkdown()
	return md.Print(markdown.PrintOptions{ToTerminal: !suppressToTerminal, ToFile: filePath})
}

func (c *RegionCosts) AsMarkdown() *markdown.Markdown {

	md := markdown.New()
	md.AddHeading(fmt.Sprintf("AWS MSK Cost Report - %s", c.Region), 1)
	md.AddParagraph(fmt.Sprintf("**Report Period:** %s to %s", c.StartDate.Format("2006-01-02"), c.EndDate.Format("2006-01-02")))
	md.AddParagraph(fmt.Sprintf("**Granularity:** %s", c.Granularity))

	// Format tags as key=value pairs, comma-separated
	md.AddParagraph("**Tags:**")
	for k, v := range c.Tags {
		md.AddParagraph(fmt.Sprintf("%s=%s", k, strings.Join(v, ",")))
	}

	md.AddParagraph(fmt.Sprintf("**Total Cost:** $%.2f USD", c.CostData.Total))

	// Usage Type Summary
	usageTypeSummary := make(map[string]map[string]float64) // service -> usageType -> cost
	serviceTotals := make(map[string]float64)               // service -> total cost
	var grandTotal float64

	for _, cost := range c.CostData.Costs {
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
	for _, cost := range c.CostData.Costs {
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

	// build info section
	md.AddHeading("KCP Build Info", 2)
	c.addBuildInfoSection(md)

	// Save to file
	return md
}

func (c *RegionCosts) addBuildInfoSection(md *markdown.Markdown) {
	md.AddParagraph(fmt.Sprintf("**Version:** %s", c.KcpBuildInfo.Version))
	md.AddParagraph(fmt.Sprintf("**Commit:** %s", c.KcpBuildInfo.Commit))
	md.AddParagraph(fmt.Sprintf("**Date:** %s", c.KcpBuildInfo.Date))
}

func (c *RegionCosts) AsCSVRecords() [][]string {
	records := [][]string{}

	records = append(records, []string{"SUMMARY", "", "", "", ""})
	records = append(records, []string{"Service", "Usage Type", "Total Cost (USD)", "", ""})

	// Calculate summary data
	usageTypeSummary := make(map[string]map[string]float64) // service -> usageType -> cost
	serviceTotals := make(map[string]float64)               // service -> total cost
	var grandTotal float64

	for _, cost := range c.CostData.Costs {
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
			records = append(records, []string{service, usageType, fmt.Sprintf("%.2f", totalCost), "", ""})
		}
		// Add service total row
		records = append(records, []string{service, "TOTAL", fmt.Sprintf("%.2f", serviceTotals[service]), "", ""})
		// Add empty row for spacing
		records = append(records, []string{"", "", "", "", ""})
	}

	// Add grand total at the bottom
	records = append(records, []string{"GRAND TOTAL", "", fmt.Sprintf("%.2f", grandTotal), "", ""})

	// Add empty row between summary and detailed breakdown
	records = append(records, []string{"", "", "", "", ""})
	records = append(records, []string{"", "", "", "", ""})

	// Write detailed breakdown section
	records = append(records, []string{"DETAILED BREAKDOWN", "", "", "", ""})
	header := []string{"Time Period Start", "Time Period End", "Service", "Usage Type", "Cost (USD)"}
	records = append(records, header)

	// Write cost data
	for _, cost := range c.CostData.Costs {
		record := []string{
			cost.TimePeriodStart,
			cost.TimePeriodEnd,
			cost.Service,
			cost.UsageType,
			fmt.Sprintf("%.2f", cost.Cost),
		}
		records = append(records, record)
	}

	return records
}

func (c *RegionCosts) WriteAsCSV() error {

	dirPath := c.GetDirPath()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := c.GetCSVPath()

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	records := c.AsCSVRecords()

	for _, record := range records {
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write CSV record: %v", err)
		}
	}
	return nil

}
