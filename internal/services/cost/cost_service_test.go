package cost

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCostQueryInfo(t *testing.T) {
	region := "us-east-1"
	start := aws.String("2025-03-10")
	end := aws.String("2026-03-10")
	granularity := costexplorertypes.GranularityDaily
	services := []string{
		types.ServiceMSK,
		types.ServiceEC2Other,
		types.ServiceAWSCertificateManager,
	}
	metrics := []string{
		"UnblendedCost",
		"BlendedCost",
		"AmortizedCost",
		"NetAmortizedCost",
		"NetUnblendedCost",
	}

	t.Run("without tags", func(t *testing.T) {
		queryInfo := buildCostQueryInfo(region, start, end, granularity, services, metrics, nil)

		// Verify TimePeriod
		assert.Equal(t, *start, queryInfo.TimePeriod.Start)
		assert.Equal(t, *end, queryInfo.TimePeriod.End)

		// Verify Granularity
		assert.Equal(t, "DAILY", queryInfo.Granularity)

		// Verify Services
		assert.Equal(t, services, queryInfo.Services)

		// Verify Regions
		assert.Equal(t, []string{region}, queryInfo.Regions)

		// Verify GroupBy
		assert.Equal(t, []string{"Service", "UsageType"}, queryInfo.GroupBy)

		// Verify Metrics
		assert.Equal(t, metrics, queryInfo.Metrics)

		// Verify Tags
		assert.Nil(t, queryInfo.Tags)

		// Verify AggregationNote
		expectedNote := "KCP aggregates daily results by summing costs per usage type over the selected time range and computing average/min/max statistics. The raw AWS output shows individual daily line items."
		assert.Equal(t, expectedNote, queryInfo.AggregationNote)

		// Verify AWS CLI command format
		assert.Contains(t, queryInfo.AWSCLICommand, "aws ce get-cost-and-usage")
		assert.Contains(t, queryInfo.AWSCLICommand, "--time-period Start=2025-03-10,End=2026-03-10")
		assert.Contains(t, queryInfo.AWSCLICommand, "--granularity DAILY")
		assert.Contains(t, queryInfo.AWSCLICommand, "--metrics UnblendedCost BlendedCost AmortizedCost NetAmortizedCost NetUnblendedCost")
		assert.Contains(t, queryInfo.AWSCLICommand, "--group-by Type=DIMENSION,Key=SERVICE Type=DIMENSION,Key=USAGE_TYPE")

		// Verify filter contains region and services
		assert.Contains(t, queryInfo.AWSCLICommand, `"REGION"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"us-east-1"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"Amazon Managed Streaming for Apache Kafka"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"EC2 - Other"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"AWS Certificate Manager"`)

		// Verify console URL format
		assert.Contains(t, queryInfo.ConsoleURL, "us-east-1.console.aws.amazon.com/costmanagement/home#/cost-explorer")
		assert.Contains(t, queryInfo.ConsoleURL, "startDate=2025-03-10")
		assert.Contains(t, queryInfo.ConsoleURL, "endDate=2026-03-10")
		assert.Contains(t, queryInfo.ConsoleURL, "granularity=Daily")
		// Verify filter contains encoded service and region values
		assert.Contains(t, queryInfo.ConsoleURL, "filter=")
		assert.Contains(t, queryInfo.ConsoleURL, "Amazon%20Managed%20Streaming%20for%20Apache%20Kafka")
		assert.Contains(t, queryInfo.ConsoleURL, "us-east-1")
	})

	t.Run("with tags", func(t *testing.T) {
		tags := map[string][]string{
			"Environment": {"production", "staging"},
			"Team":        {"platform"},
		}

		queryInfo := buildCostQueryInfo(region, start, end, granularity, services, metrics, tags)

		// Verify Tags
		assert.Equal(t, tags, queryInfo.Tags)

		// Verify CLI command includes tags in filter
		assert.Contains(t, queryInfo.AWSCLICommand, `"Tags"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"Environment"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"production"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"staging"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"Team"`)
		assert.Contains(t, queryInfo.AWSCLICommand, `"platform"`)

		// Verify filter structure has And with all conditions
		assert.Contains(t, queryInfo.AWSCLICommand, `{"And":`)

		// Verify deterministic output across multiple calls
		for i := 0; i < 10; i++ {
			q := buildCostQueryInfo(region, start, end, granularity, services, metrics, tags)
			assert.Equal(t, queryInfo.AWSCLICommand, q.AWSCLICommand, "CLI command should be deterministic on iteration %d", i)
		}
	})

	t.Run("CLI command is valid JSON filter", func(t *testing.T) {
		queryInfo := buildCostQueryInfo(region, start, end, granularity, services, metrics, nil)

		// Extract the filter JSON from the CLI command
		startIdx := strings.Index(queryInfo.AWSCLICommand, "--filter '")
		require.NotEqual(t, -1, startIdx)
		startIdx += len("--filter '")

		endIdx := strings.Index(queryInfo.AWSCLICommand[startIdx:], "' \\")
		require.NotEqual(t, -1, endIdx)

		filterJSON := queryInfo.AWSCLICommand[startIdx : startIdx+endIdx]

		// Verify it's valid JSON
		var filter map[string]interface{}
		err := json.Unmarshal([]byte(filterJSON), &filter)
		require.NoError(t, err, "Filter should be valid JSON")

		// Verify structure
		assert.Contains(t, filter, "And")
		andFilters := filter["And"].([]interface{})
		assert.Len(t, andFilters, 2) // Region + Service filters
	})

	t.Run("with hourly granularity", func(t *testing.T) {
		hourlyGranularity := costexplorertypes.GranularityHourly
		queryInfo := buildCostQueryInfo(region, start, end, hourlyGranularity, services, metrics, nil)

		assert.Equal(t, "HOURLY", queryInfo.Granularity)
		assert.Contains(t, queryInfo.AWSCLICommand, "--granularity HOURLY")
	})

	t.Run("with monthly granularity", func(t *testing.T) {
		monthlyGranularity := costexplorertypes.GranularityMonthly
		queryInfo := buildCostQueryInfo(region, start, end, monthlyGranularity, services, metrics, nil)

		assert.Equal(t, "MONTHLY", queryInfo.Granularity)
		assert.Contains(t, queryInfo.AWSCLICommand, "--granularity MONTHLY")
	})
}

func TestBuildJSONArray(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{
			name:     "single item",
			input:    []string{"item1"},
			expected: `"item1"`,
		},
		{
			name:     "multiple items",
			input:    []string{"item1", "item2", "item3"},
			expected: `"item1","item2","item3"`,
		},
		{
			name:     "empty slice",
			input:    []string{},
			expected: "",
		},
		{
			name:     "items with spaces",
			input:    []string{"Amazon MSK", "EC2 - Other"},
			expected: `"Amazon MSK","EC2 - Other"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildJSONArray(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConsoleFilterEncode(t *testing.T) {
	input := `[{"dimension":"Service"}]`
	expected := `%5B%7B%22dimension%22:%22Service%22%7D%5D`
	assert.Equal(t, expected, consoleFilterEncode(input))
}

func TestBuildConsoleURL(t *testing.T) {
	url := buildConsoleURL("us-east-1", "2025-03-10", "2026-03-10", []string{"Amazon MSK"})

	// Verify base URL structure
	assert.Contains(t, url, "us-east-1.console.aws.amazon.com/costmanagement/home#/cost-explorer")

	// Verify date params
	assert.Contains(t, url, "startDate=2025-03-10")
	assert.Contains(t, url, "endDate=2026-03-10")

	// Verify filter uses correct encoded format with dimension id/displayValue and operator
	assert.Contains(t, url, "%22id%22:%22Service%22")
	assert.Contains(t, url, "%22operator%22:%22INCLUDES%22")
	assert.Contains(t, url, "%22id%22:%22Region%22")
	assert.Contains(t, url, "Amazon%20MSK")

	// Verify other required params
	assert.Contains(t, url, "historicalRelativeRange=CUSTOM")
	assert.Contains(t, url, "reportMode=STANDARD")
	assert.Contains(t, url, "chartStyle=STACK")
}
