package report

import (
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/types"
)

type ReportService struct{}

func NewReportService() *ReportService {
	return &ReportService{}
}

func (rs *ReportService) ProcessCosts(region types.DiscoveredRegion) types.ProcessedRegionCosts {
	var processedCosts []types.ParsedCost
	serviceTotals := make(map[string]float64)

	for _, result := range region.Costs.CostResults {
		if result.TimePeriod == nil {
			continue
		}

		start := aws.ToString(result.TimePeriod.Start)
		end := aws.ToString(result.TimePeriod.End)

		for _, group := range result.Groups {
			if len(group.Keys) < 2 || group.Metrics == nil {
				continue
			}

			service := aws.ToString(&group.Keys[0])
			lineItem := aws.ToString(&group.Keys[1])

			// Get the UnblendedCost from metrics
			if unblendedCost, exists := group.Metrics["UnblendedCost"]; exists && unblendedCost.Amount != nil {
				cost := aws.ToString(unblendedCost.Amount)

				processedCosts = append(processedCosts, types.ParsedCost{
					Start:    start,
					End:      end,
					Service:  service,
					LineItem: lineItem,
					Cost:     cost,
				})

				// Parse the cost as float64 and add to service total
				if costFloat, err := strconv.ParseFloat(cost, 64); err == nil {
					serviceTotals[service] += costFloat
				}
			}
		}
	}

	// Convert service totals to the required format
	var totals []types.ServiceTotal
	for service, total := range serviceTotals {
		totals = append(totals, types.ServiceTotal{
			Service: service,
			Total:   strconv.FormatFloat(total, 'f', -1, 64),
		})
	}

	return types.ProcessedRegionCosts{
		Region:   region.Name,
		Costs:    processedCosts,
		Totals:   totals,
		Metadata: region.Costs.CostMetadata,
	}
}

func (rs *ReportService) ProcessMetrics(cluster types.DiscoveredCluster) types.ProcessedClusterMetrics {
	var processedMetrics []types.ProcessedMetric

	// Iterate through each metric result
	for _, result := range cluster.ClusterMetrics.Results {
		label := result.Label
		if label == nil {
			continue
		}

		// Handle case where there are no timestamps/values (empty arrays)
		if len(result.Timestamps) == 0 || len(result.Values) == 0 {
			// Add a single entry with null timestamp and value
			processedMetrics = append(processedMetrics, types.ProcessedMetric{
				Label:     *label,
				Timestamp: nil,
				Value:     nil,
			})
			continue
		}

		// Iterate through timestamps and values (they should be paired)
		for i, timestamp := range result.Timestamps {
			// Ensure we don't go out of bounds for values
			if i >= len(result.Values) {
				break
			}

			// Convert timestamp to string
			timestampStr := timestamp.Format("2006-01-02T15:04:05Z")
			value := result.Values[i]

			processedMetrics = append(processedMetrics, types.ProcessedMetric{
				Label:     *label,
				Timestamp: &timestampStr,
				Value:     &value,
			})
		}
	}

	return types.ProcessedClusterMetrics{
		ClusterName: cluster.Name,
		ClusterArn:  cluster.Arn,
		Metrics:     processedMetrics,
		Metadata:    cluster.ClusterMetrics.MetricMetadata,
	}
}
