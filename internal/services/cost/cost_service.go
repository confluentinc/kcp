package cost

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/confluentinc/kcp/internal/types"
)

type CostService struct {
	client *costexplorer.Client
}

func NewCostService(client *costexplorer.Client) *CostService {
	return &CostService{
		client: client,
	}
}

func (cs *CostService) GetCostsForTimeRange(ctx context.Context, region string, startDate time.Time, endDate time.Time, granularity costexplorertypes.Granularity, tags map[string][]string) (types.CostInformation, error) {
	slog.Info("💰 getting AWS costs", "region", region, "start", startDate, "end", endDate, "granularity", granularity, "tags", tags)

	startStr := aws.String(startDate.Format("2006-01-02"))
	endStr := aws.String(endDate.Format("2006-01-02"))

	if granularity == costexplorertypes.GranularityHourly {
		startStr = aws.String(startDate.Format("2006-01-02T00:00:00Z"))
		endStr = aws.String(endDate.Format("2006-01-02T00:00:00Z"))
	}

	services := []string{"Amazon Managed Streaming for Apache Kafka", "EC2 - Other", "AWS Certificate Manager"}

	metrics := []string{
		string(costexplorertypes.MetricUnblendedCost),
		string(costexplorertypes.MetricBlendedCost),
		string(costexplorertypes.MetricAmortizedCost),
		string(costexplorertypes.MetricNetAmortizedCost),
		string(costexplorertypes.MetricNetUnblendedCost),
	}

	// Collect all results across pages
	var allResults []costexplorertypes.ResultByTime
	var nextToken *string
	for {
		input := cs.buildCostExplorerInput(region, startStr, endStr, granularity, services, tags, nextToken)

		output, err := cs.client.GetCostAndUsage(ctx, input)
		if err != nil {
			return types.CostInformation{}, fmt.Errorf("failed to get cost and usage: %v", err)
		}

		// Append results from this page
		allResults = append(allResults, output.ResultsByTime...)

		// Check if there are more pages
		if output.NextPageToken == nil {
			break
		}

		nextToken = output.NextPageToken
	}

	// Build query info with CLI command and console URL
	queryInfo := buildCostQueryInfo(region, startStr, endStr, granularity, services, metrics, tags)

	costInformation := types.CostInformation{
		CostMetadata: types.CostMetadata{
			StartDate:   startDate,
			EndDate:     endDate,
			Granularity: string(granularity),
			Tags:        tags,
			Services:    services,
		},
		CostResults: allResults,
		QueryInfo:   queryInfo,
	}

	return costInformation, nil
}

func (cs *CostService) buildCostExplorerInput(region string, start, end *string, granularity costexplorertypes.Granularity, services []string, tags map[string][]string, nextToken *string) *costexplorer.GetCostAndUsageInput {
	timePeriod := &costexplorertypes.DateInterval{
		Start: start,
		End:   end,
	}

	filter := &costexplorertypes.Expression{
		And: []costexplorertypes.Expression{
			{
				Dimensions: &costexplorertypes.DimensionValues{
					Key:    costexplorertypes.DimensionRegion,
					Values: []string{region},
				},
			},
			{
				Dimensions: &costexplorertypes.DimensionValues{
					Key:    costexplorertypes.DimensionService,
					Values: services,
				},
			},
		},
	}

	// https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_GetDimensionValues.html#API_GetDimensionValues_RequestSyntax
	if len(tags) > 0 {
		for key, values := range tags {
			filter.And = append(filter.And, costexplorertypes.Expression{
				Tags: &costexplorertypes.TagValues{
					Key:    aws.String(key),
					Values: values,
				},
			})
		}
	}

	metrics := []string{
		string(costexplorertypes.MetricUnblendedCost),
		string(costexplorertypes.MetricBlendedCost),
		string(costexplorertypes.MetricAmortizedCost),
		string(costexplorertypes.MetricNetAmortizedCost),
		string(costexplorertypes.MetricNetUnblendedCost),
	}

	groupBy := []costexplorertypes.GroupDefinition{
		{
			Type: costexplorertypes.GroupDefinitionTypeDimension,
			Key:  aws.String(string(costexplorertypes.MonitorDimensionService)),
		},
		{
			Type: costexplorertypes.GroupDefinitionTypeDimension,
			Key:  aws.String(string(costexplorertypes.DimensionUsageType)),
		},
	}

	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod:  timePeriod,
		Granularity: granularity,
		Filter:      filter,
		Metrics:     metrics,
		GroupBy:     groupBy,
	}

	// Add NextPageToken if provided for pagination
	if nextToken != nil {
		input.NextPageToken = nextToken
	}

	return input
}

// buildCostQueryInfo generates structured query information including AWS CLI command and console URL
func buildCostQueryInfo(region string, start, end *string, granularity costexplorertypes.Granularity, services []string, metrics []string, tags map[string][]string) types.CostQueryInfo {
	// Build filter JSON for CLI command
	filterParts := []string{
		fmt.Sprintf(`{"Dimensions":{"Key":"REGION","Values":["%s"]}}`, region),
		fmt.Sprintf(`{"Dimensions":{"Key":"SERVICE","Values":[%s]}}`, buildJSONArray(services)),
	}

	// Add tags to filter if present
	for key, values := range tags {
		filterParts = append(filterParts, fmt.Sprintf(`{"Tags":{"Key":"%s","Values":[%s]}}`, key, buildJSONArray(values)))
	}

	filterJSON := fmt.Sprintf(`{"And":[%s]}`, strings.Join(filterParts, ","))

	// Build AWS CLI command
	cliCommand := fmt.Sprintf(`aws ce get-cost-and-usage \
  --time-period Start=%s,End=%s \
  --granularity %s \
  --filter '%s' \
  --metrics %s \
  --group-by Type=DIMENSION,Key=SERVICE Type=DIMENSION,Key=USAGE_TYPE`,
		*start,
		*end,
		strings.ToUpper(string(granularity)),
		filterJSON,
		strings.Join(metrics, " "))

	consoleURL := buildConsoleURL(region, *start, *end, services)

	return types.CostQueryInfo{
		TimePeriod: types.CostQueryTimePeriod{
			Start: *start,
			End:   *end,
		},
		Granularity:     string(granularity),
		Services:        services,
		Regions:         []string{region},
		GroupBy:         []string{"Service", "UsageType"},
		Metrics:         metrics,
		Tags:            tags,
		AWSCLICommand:   cliCommand,
		ConsoleURL:      consoleURL,
		AggregationNote: "KCP aggregates daily results by summing costs per usage type over the selected time range and computing average/min/max statistics. The raw AWS output shows individual daily line items.",
	}
}

// buildJSONArray converts a string slice to a JSON array string for CLI commands
func buildJSONArray(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf(`"%s"`, item)
	}
	return strings.Join(quoted, ",")
}

// buildConsoleURL generates a pre-filled AWS Cost Explorer console URL.
// The filter format uses dimension id/displayValue pairs with an INCLUDES operator,
// matching the format the AWS console produces when you configure filters manually.
func buildConsoleURL(region, startDate, endDate string, services []string) string {
	// Build service filter values
	var serviceValues []string
	for _, svc := range services {
		serviceValues = append(serviceValues, fmt.Sprintf(
			`{"value":"%s","displayValue":"%s"}`, svc, svc,
		))
	}

	// Build the filter JSON with the format AWS Console expects
	filter := fmt.Sprintf(
		`[{"dimension":{"id":"Service","displayValue":"Service"},"operator":"INCLUDES","values":[%s]},{"dimension":{"id":"Region","displayValue":"Region"},"operator":"INCLUDES","values":[{"value":"%s","displayValue":"%s"}]}]`,
		strings.Join(serviceValues, ","),
		region,
		region,
	)

	// Encode for use in a URL fragment: encode [ ] { } " and spaces,
	// but leave : and , as-is (matches how AWS console encodes these URLs)
	encodedFilter := consoleFilterEncode(filter)

	return fmt.Sprintf(
		"https://%s.console.aws.amazon.com/costmanagement/home#/cost-explorer?chartStyle=STACK&costAggregate=unBlendedCost&endDate=%s&excludeForecasting=true&filter=%s&futureRelativeRange=CUSTOM&granularity=Daily&groupBy=%%5B%%22Service%%22%%5D&historicalRelativeRange=CUSTOM&reportMode=STANDARD&showOnlyUncategorized=false&showOnlyUntagged=false&startDate=%s&usageAggregate=undefined&useNormalizedUnits=false",
		region,
		endDate,
		encodedFilter,
		startDate,
	)
}

// consoleFilterEncode encodes a JSON string for AWS Console URL fragments.
// Encodes [ ] { } " and spaces, but leaves : and , unencoded to match
// the format the AWS console produces.
func consoleFilterEncode(s string) string {
	r := strings.NewReplacer(
		`[`, `%5B`,
		`]`, `%5D`,
		`{`, `%7B`,
		`}`, `%7D`,
		`"`, `%22`,
		` `, `%20`,
	)
	return r.Replace(s)
}
