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

// ParseCostResults transforms AWS cost explorer results into a flattened structure
func (rs *ReportService) ParseCostResults(region string, costs types.CostInformation) types.ParsedRegionCostResponse {
	var parsedCosts []types.ParsedCost
	serviceTotals := make(map[string]float64)

	for _, result := range costs.CostResults {
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

				parsedCosts = append(parsedCosts, types.ParsedCost{
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

	// metadata := costs.CostMetadata
	// metadata.StartDate = costs.StartDate
	// metadata.EndDate = costs.EndDate
	// metadata.Granularity = costs.Granularity
	// metadata.Tags = costs.Tags
	// metadata.Services = costs.Services

	return types.ParsedRegionCostResponse{

		Region:   region,
		Costs:    parsedCosts,
		Totals:   totals,
		Metadata: costs.CostMetadata,
	}
}
