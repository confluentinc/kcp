package types

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/markdown"
)

type Cost struct {
	TimePeriodStart string  `json:"time_period_start"`
	TimePeriodEnd   string  `json:"time_period_end"`
	Service         string  `json:"service"`
	Cost            float64 `json:"cost"`
	UsageType       string  `json:"usage_type"`
}

type CostData struct {
	Costs []Cost `json:"costs"`
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
	Services     []string            `json:"services"`
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
	return c.GetDirPathWithBase("kcp-scan")
}

func (c *RegionCosts) GetDirPathWithBase(baseDir string) string {
	return filepath.Join(baseDir, c.Region)
}

func (c *RegionCosts) WriteAsJson() error {
	return c.WriteAsJsonWithBase("kcp-scan")
}

func (c *RegionCosts) WriteAsJsonWithBase(baseDir string) error {
	dirPath := c.GetDirPathWithBase(baseDir)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := filepath.Join(dirPath, fmt.Sprintf("%s-cost-report.json", c.Region))

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
	return c.WriteAsMarkdownWithBase("kcp-scan", suppressToTerminal)
}

func (c *RegionCosts) WriteAsMarkdownWithBase(baseDir string, suppressToTerminal bool) error {
	dirPath := c.GetDirPathWithBase(baseDir)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := filepath.Join(dirPath, fmt.Sprintf("%s-cost-report.md", c.Region))
	md := c.AsMarkdown()
	return md.Print(markdown.PrintOptions{ToTerminal: !suppressToTerminal, ToFile: filePath})
}

func (c *RegionCosts) AsMarkdown() *markdown.Markdown {

	md := markdown.New()
	md.AddHeading(fmt.Sprintf("AWS Service Cost Report for Region %s", c.Region), 1)

	md.AddParagraph(fmt.Sprintf("This is a report of costs retrieved from the AWS Cost Explorer API for the region **%s**.", c.Region))

	md.AddHeading("Dimensions Used", 3)

	md.AddParagraph(fmt.Sprintf("**Region:** %s", c.Region))
	md.AddParagraph(fmt.Sprintf("**Services:** %s", strings.Join(c.Services, ", ")))
	md.AddParagraph(fmt.Sprintf("**Aggregation Period:** %s to %s", c.StartDate.Format("2006-01-02"), c.EndDate.Format("2006-01-02")))
	md.AddParagraph(fmt.Sprintf("**Aggregation Granularity:** %s", c.Granularity))
	md.AddParagraph("**Resource Filter Tags:**")
	for k, v := range c.Tags {
		md.AddParagraph(fmt.Sprintf("%s=%s", k, strings.Join(v, ",")))
	}

	// Usage Type Summary
	usageTypeSummary := make(map[string]map[string]float64) // service -> usageType -> cost
	serviceTotals := make(map[string]float64)               // service -> total cost

	for _, cost := range c.CostData.Costs {
		if usageTypeSummary[cost.Service] == nil {
			usageTypeSummary[cost.Service] = make(map[string]float64)
		}
		usageTypeSummary[cost.Service][cost.UsageType] += cost.Cost
		serviceTotals[cost.Service] += cost.Cost
	}

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
			totalCostFormatted := fmt.Sprintf("$%.2f", totalCost)
			if totalCostFormatted == "$0.00" {
				continue
			}
			usageData = append(usageData, []string{service, usageType, totalCostFormatted})
		}
		// Add service total row
		usageData = append(usageData, []string{service, "**TOTAL**", fmt.Sprintf("$%.2f", serviceTotals[service])})
	}

	mskUsageData := [][]string{}
	otherUsageData := [][]string{}

	var mskDataTransferCost, ec2DataTransferCost float64

	for i := range usageData {
		if usageData[i][0] == "Amazon Managed Streaming for Apache Kafka" {
			if strings.Contains(usageData[i][1], "DataTransfer-Regional-Bytes") {
				if v, err := strconv.ParseFloat(strings.TrimPrefix(usageData[i][2], "$"), 64); err == nil {
					mskDataTransferCost = v
				}
			}
			mskUsageData = append(mskUsageData, []string{usageData[i][1], usageData[i][2]})
		} else {
			if strings.Contains(usageData[i][1], "DataTransfer-Regional-Bytes") && usageData[i][0] == "EC2 - Other" {
				if v, err := strconv.ParseFloat(strings.TrimPrefix(usageData[i][2], "$"), 64); err == nil {
					ec2DataTransferCost = v
				}
			}
			otherUsageData = append(otherUsageData, usageData[i])
		}
	}

	mskUsageHeaders := []string{"Usage Type", "Total Cost (USD)"}
	md.AddHeading("Amazon Managed Streaming for Apache Kafka (MSK) Service Costs Summary", 2)
	md.AddParagraph("This section presents a summary of directly attributable costs for the Amazon Managed Streaming for Apache Kafka (MSK) service.")
	md.AddTable(mskUsageHeaders, mskUsageData, 0, 1)

	otherUsageHeaders := []string{"Service", "Usage Type", "Total Cost (USD)"}
	md.AddHeading("Other Services Costs Summary", 2)
	md.AddParagraph("This section details costs for additional AWS services. " +
		"Some portions of some costs may be indirectly attributable to Amazon MSK operations, while others may arise from unrelated service activities. " +
		"These *hidden* costs are not identifiable through AWS Cost APIs without comprehensive resource tagging across all resources used by MSK operations.")
	md.AddTable(otherUsageHeaders, otherUsageData, 0, 1)

	md.AddHeading("Estimated Amazon MSK Hidden Costs", 2)
	md.AddParagraph("This section details potential *hidden* costs of Amazon MSK operations.  " +
		"These are costs attributed to other AWS services, which may be caused by the operations of Amazon MSK, but are not directly attributable to the service itself.")

	hiddenHeaders := []string{"Hidden Cost Type", "Description", "Service", "Hidden Cost (USD)"}
	hiddenCosts := [][]string{}

	crossAZDataTransferCosts := []string{
		"Cross AZ Data Transfer costs",
		fmt.Sprintf("$%.2f of the $%.2f **EC2 - Other:DataTransfer-Regional-Bytes** cost is MSK-attributable (equivalent to **MSK:DataTransfer-Regional-Bytes** cost).", mskDataTransferCost, ec2DataTransferCost),
		"EC2 - Other",
		fmt.Sprintf("$%.2f", mskDataTransferCost)}

	hiddenCosts = append(hiddenCosts, crossAZDataTransferCosts)

	md.AddTable(hiddenHeaders, hiddenCosts, 0, 1, 2)

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
	return c.WriteAsCSVWithBase("kcp-scan")
}

func (c *RegionCosts) WriteAsCSVWithBase(baseDir string) error {
	dirPath := c.GetDirPathWithBase(baseDir)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := filepath.Join(dirPath, fmt.Sprintf("%s-cost-report.csv", c.Region))

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
