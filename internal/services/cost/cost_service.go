package cost

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
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

func (cs *CostService) GetCostsForTimeRange(region string, startDate time.Time, endDate time.Time, granularity costexplorertypes.Granularity, tags map[string][]string) (types.RegionCosts, error) {
	slog.Info("💰 getting AWS costs", "region", region, "start", startDate, "end", endDate, "granularity", granularity, "tags", tags)

	startStr := aws.String(startDate.Format("2006-01-02"))
	endStr := aws.String(endDate.Format("2006-01-02"))

	if granularity == costexplorertypes.GranularityHourly {
		startStr = aws.String(startDate.Format("2006-01-02T00:00:00Z"))
		endStr = aws.String(endDate.Format("2006-01-02T00:00:00Z"))
	}

	input := cs.buildCostExplorerInput(region, startStr, endStr, granularity, tags)

	output, err := cs.client.GetCostAndUsage(context.Background(), input)
	if err != nil {
		return types.RegionCosts{}, fmt.Errorf("failed to get cost and usage: %v", err)
	}

	costData := cs.processCostExplorerOutput(output)
	regionCosts := types.RegionCosts{
		Region:      region,
		CostData:    costData,
		StartDate:   startDate,
		EndDate:     endDate,
		Granularity: string(granularity),
		Tags:        tags,
	}
	return regionCosts, nil
}

func (cs *CostService) buildCostExplorerInput(region string, start, end *string, granularity costexplorertypes.Granularity, tags map[string][]string) *costexplorer.GetCostAndUsageInput {
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
					Values: []string{"Amazon Managed Streaming for Apache Kafka", "EC2 - Other", "AWS Certificate Manager"},
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

	return &costexplorer.GetCostAndUsageInput{
		TimePeriod: &costexplorertypes.DateInterval{
			Start: start,
			End:   end,
		},
		Granularity: granularity,
		Filter:      filter,
		Metrics:     []string{"UnblendedCost"},
		GroupBy: []costexplorertypes.GroupDefinition{
			{
				Type: costexplorertypes.GroupDefinitionTypeDimension,
				Key:  aws.String("SERVICE"),
			},
			{
				Type: costexplorertypes.GroupDefinitionTypeDimension,
				Key:  aws.String("USAGE_TYPE"),
			},
		},
	}
}

func (cs *CostService) processCostExplorerOutput(output *costexplorer.GetCostAndUsageOutput) types.CostData {
	var costs []types.Cost

	for _, result := range output.ResultsByTime {
		for _, group := range result.Groups {
			cost, err := strconv.ParseFloat(*group.Metrics["UnblendedCost"].Amount, 64)
			if err != nil {
				slog.Error("Failed to parse cost amount", "error", err)
				continue
			}

			// Extract service and usage type from group keys
			// Keys[0] should be SERVICE, Keys[1] should be USAGE_TYPE
			service := ""
			usageType := ""
			if len(group.Keys) >= 2 {
				service = group.Keys[0]
				usageType = group.Keys[1]
			} else if len(group.Keys) == 1 {
				usageType = group.Keys[0]
			}

			// Multiply cost by 2 if usage type contains "DataTransfer-Regional-Bytes"
			if strings.Contains(usageType, "DataTransfer-Regional-Bytes") {
				usageType = usageType + " (cross AZ data transfer)"
			}

			costs = append(costs, types.Cost{
				TimePeriodStart: *result.TimePeriod.Start,
				TimePeriodEnd:   *result.TimePeriod.End,
				Service:         service,
				UsageType:       usageType,
				Cost:            cost,
			})
		}
	}

	return types.CostData{
		Costs: costs,
	}
}
