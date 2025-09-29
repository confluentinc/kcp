package cost

import (
	"context"
	"fmt"
	"log/slog"
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

	costInformation := types.CostInformation{
		CostMetadata: types.CostMetadata{
			StartDate:   startDate,
			EndDate:     endDate,
			Granularity: string(granularity),
			Tags:        tags,
			Services:    services,
		},
		CostResults: allResults,
	}

	return costInformation, nil
}

func (cs *CostService) buildCostExplorerInput(region string, start, end *string, granularity costexplorertypes.Granularity, services []string, tags map[string][]string, nextToken *string) *costexplorer.GetCostAndUsageInput {
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
		string(costexplorertypes.MetricUsageQuantity),
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
		TimePeriod: &costexplorertypes.DateInterval{
			Start: start,
			End:   end,
		},
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
