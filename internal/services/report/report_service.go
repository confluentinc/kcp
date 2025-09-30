package report

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/confluentinc/kcp/internal/types"
)

type ReportService struct{}

func NewReportService() *ReportService {
	return &ReportService{}
}

func (rs *ReportService) ProcessState(state types.State) types.ProcessedState {
	processedRegions := []types.ProcessedRegion{}

	// Process each region: flatten costs and metrics for frontend consumption
	for _, region := range state.Regions {
		// Flatten cost data from nested AWS Cost Explorer format
		processedCosts := rs.flattenCosts(region)

		// Process each cluster's metrics
		processedClusters := []types.ProcessedCluster{}
		for _, cluster := range region.Clusters {
			// Flatten metrics data from nested CloudWatch format
			processedMetrics := rs.flattenMetrics(cluster)

			processedClusters = append(processedClusters, types.ProcessedCluster{
				Name:                        cluster.Name,
				Arn:                         cluster.Arn,
				ClusterMetrics:              processedMetrics,
				AWSClientInformation:        cluster.AWSClientInformation,
				KafkaAdminClientInformation: cluster.KafkaAdminClientInformation,
			})
		}

		processedRegions = append(processedRegions, types.ProcessedRegion{
			Name:           region.Name,
			Configurations: region.Configurations,
			Costs:          processedCosts,
			Clusters:       processedClusters,
		})
	}

	// Return the processed state with flattened data for frontend consumption
	processedState := types.ProcessedState{
		Regions:      processedRegions,
		KcpBuildInfo: state.KcpBuildInfo,
		Timestamp:    state.Timestamp,
	}

	return processedState
}

// FilterRegionCosts filters the processed state to return cost data for a specific region
func (rs *ReportService) FilterRegionCosts(processedState types.ProcessedState, regionName string, startTime, endTime *time.Time) (*types.ProcessedRegionCosts, error) {
	// Find the specified region
	var targetRegion *types.ProcessedRegion
	for _, r := range processedState.Regions {
		if strings.EqualFold(r.Name, regionName) {
			targetRegion = &r
			break
		}
	}
	if targetRegion == nil {
		return nil, fmt.Errorf("region '%s' not found", regionName)
	}

	// Start with the region's cost data
	regionCosts := targetRegion.Costs

	// If no date filters, use all costs but still calculate aggregates
	var filteredCosts []types.ProcessedCost
	if startTime == nil && endTime == nil {
		filteredCosts = regionCosts.Results
	} else {
		// Filter costs by date range
		for _, cost := range regionCosts.Results {
			// Parse the cost start time - costs use YYYY-MM-DD format
			costStartTime, err := time.Parse("2006-01-02", cost.Start)
			if err != nil {
				// Try alternative formats if the first one fails
				if costStartTime, err = time.Parse("2006-01-02T15:04:05Z", cost.Start); err != nil {
					if costStartTime, err = time.Parse(time.RFC3339, cost.Start); err != nil {
						// Skip costs with invalid timestamps
						continue
					}
				}
			}

			// Apply date filters
			if startTime != nil && costStartTime.Before(*startTime) {
				continue
			}
			if endTime != nil && costStartTime.After(*endTime) {
				continue
			}

			filteredCosts = append(filteredCosts, cost)
		}
	}

	// Calculate aggregates from filtered costs
	aggregates := rs.calculateCostAggregates(filteredCosts)

	return &types.ProcessedRegionCosts{
		Region:     regionName,
		Metadata:   regionCosts.Metadata,
		Results:    filteredCosts,
		Aggregates: aggregates,
	}, nil
}

// TODO we should ask for clusterArn instead of clusterName

// filterClusterMetrics filters the processed state by region, clusterArn, and date range
func (rs *ReportService) FilterClusterMetrics(processedState types.ProcessedState, clusterArn string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error) {
	var regionName string
	var targetCluster *types.ProcessedCluster

	for _, region := range processedState.Regions {
		for _, cluster := range region.Clusters {
			if strings.EqualFold(cluster.Arn, clusterArn) {
				targetCluster = &cluster
				regionName = region.Name
				break
			}
		}
	}

	if targetCluster == nil {
		return nil, fmt.Errorf("cluster '%s' not found", clusterArn)
	}

	var filteredMetrics []types.ProcessedMetric

	// If no date filters, use all metrics
	if startTime == nil && endTime == nil {
		filteredMetrics = targetCluster.ClusterMetrics.Metrics
	} else {
		// Filter metrics by date range
		for _, metric := range targetCluster.ClusterMetrics.Metrics {
			// Parse the metric start time
			metricStartTime, err := time.Parse("2006-01-02T15:04:05Z", metric.Start)
			if err != nil {
				// Skip metrics with invalid timestamps
				continue
			}

			// Apply date filters
			if startTime != nil && metricStartTime.Before(*startTime) {
				continue
			}
			if endTime != nil && metricStartTime.After(*endTime) {
				continue
			}

			filteredMetrics = append(filteredMetrics, metric)
		}
	}

	// Calculate aggregates from filtered metrics
	aggregates := rs.calculateMetricsAggregates(filteredMetrics)

	return &types.ProcessedClusterMetrics{
		Region:     regionName,
		ClusterArn: clusterArn,
		Metadata:   targetCluster.ClusterMetrics.Metadata,
		Metrics:    filteredMetrics,
		Aggregates: aggregates,
	}, nil
}

// filterMetrics filters the processed state by region, cluster, and date range
func (rs *ReportService) FilterMetrics(processedState types.ProcessedState, regionName, clusterName string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error) {
	// Find the specified region
	var targetRegion *types.ProcessedRegion
	for _, r := range processedState.Regions {
		if strings.EqualFold(r.Name, regionName) {
			targetRegion = &r
			break
		}
	}
	if targetRegion == nil {
		return nil, fmt.Errorf("region '%s' not found", regionName)
	}

	// Find the specified cluster within the region
	var targetCluster *types.ProcessedCluster
	for _, c := range targetRegion.Clusters {
		if strings.EqualFold(c.Name, clusterName) {
			targetCluster = &c
			break
		}
	}
	if targetCluster == nil {
		return nil, fmt.Errorf("cluster '%s' not found in region '%s'", clusterName, regionName)
	}

	var filteredMetrics []types.ProcessedMetric

	// If no date filters, use all metrics
	if startTime == nil && endTime == nil {
		filteredMetrics = targetCluster.ClusterMetrics.Metrics
	} else {
		// Filter metrics by date range
		for _, metric := range targetCluster.ClusterMetrics.Metrics {
			// Parse the metric start time
			metricStartTime, err := time.Parse("2006-01-02T15:04:05Z", metric.Start)
			if err != nil {
				// Skip metrics with invalid timestamps
				continue
			}

			// Apply date filters
			if startTime != nil && metricStartTime.Before(*startTime) {
				continue
			}
			if endTime != nil && metricStartTime.After(*endTime) {
				continue
			}

			filteredMetrics = append(filteredMetrics, metric)
		}
	}

	// Calculate aggregates from filtered metrics
	aggregates := rs.calculateMetricsAggregates(filteredMetrics)

	return &types.ProcessedClusterMetrics{
		Metadata:   targetCluster.ClusterMetrics.Metadata,
		Metrics:    filteredMetrics,
		Aggregates: aggregates,
	}, nil
}

// calculateCostAggregates takes raw cost data and calculates statistics for each unique combination
// of service + metric type + usage type, then organizes it into the final nested structure
func (rs *ReportService) calculateCostAggregates(costs []types.ProcessedCost) types.ProcessedAggregates {
	aggregates := types.NewProcessedAggregates()

	if len(costs) == 0 {
		return aggregates
	}

	type MetricData struct {
		Service    string    // eg "AWS Certificate Manager"
		MetricName string    // eg "unblended_cost"
		UsageType  string    // eg "USE1-FreePrivateCA"
		Values     []float64 // eg [1.50, 2.25, 0.75] - multiple cost entries over time
	}

	// PHASE 1: Group all cost data by unique combinations
	var groupedData []*MetricData           // Final list of all unique combinations
	dataMap := make(map[string]*MetricData) // Temporary map to find existing combinations

	for _, cost := range costs {
		service := cost.Service
		usageType := cost.UsageType

		// Extract all 6 cost metrics from this single cost entry
		metrics := map[string]float64{
			"unblended_cost":     cost.Values.UnblendedCost,
			"blended_cost":       cost.Values.BlendedCost,
			"amortized_cost":     cost.Values.AmortizedCost,
			"net_amortized_cost": cost.Values.NetAmortizedCost,
			"net_unblended_cost": cost.Values.NetUnblendedCost,
		}

		// For each of the 6 metrics, create or update a MetricData entry
		for metricName, value := range metrics {
			// Create unique key: "AWS Certificate Manager|unblended_cost|USE1-FreePrivateCA"
			key := service + "|" + metricName + "|" + usageType

			if data, exists := dataMap[key]; exists {
				// This combination already exists, add this value to the existing list
				data.Values = append(data.Values, value)
			} else {
				// First time seeing this combination, create new MetricData
				newData := &MetricData{
					Service:    service,
					MetricName: metricName,
					UsageType:  usageType,
					Values:     []float64{value},
				}
				dataMap[key] = newData
				groupedData = append(groupedData, newData)
			}
		}
	}

	// PHASE 2: Calculate statistics for each grouped combination
	serviceTotals := make(map[string]float64) // Track totals per service+metric

	// Process each unique combination and calculate its statistics
	for _, data := range groupedData {
		if len(data.Values) == 0 {
			continue // Skip empty groups (shouldn't happen)
		}

		// Calculate sum, min, max from all values in this group
		sum := 0.0
		min := data.Values[0] // Start with first value
		max := data.Values[0] // Start with first value

		for _, value := range data.Values {
			sum += value
			if value < min {
				min = value
			}
			if value > max {
				max = value
			}
		}

		avg := sum / float64(len(data.Values)) // Calculate average

		// Create the final aggregate structure with all statistics
		costAggregate := types.CostAggregate{
			Sum:     &sum,
			Average: &avg,
			Maximum: &max,
			Minimum: &min,
		}

		// Assign this aggregate to the correct service and metric field
		// Example: aggregates.AWSCertificateManager.UnblendedCost["USE1-FreePrivateCA"] = costAggregate
		switch data.Service {
		case "AWS Certificate Manager":
			rs.assignToServiceMetric(&aggregates.AWSCertificateManager, data.MetricName, data.UsageType, costAggregate)
		case "Amazon Managed Streaming for Apache Kafka":
			rs.assignToServiceMetric(&aggregates.AmazonManagedStreamingForApacheKafka, data.MetricName, data.UsageType, costAggregate)
		case "EC2 - Other":
			rs.assignToServiceMetric(&aggregates.EC2Other, data.MetricName, data.UsageType, costAggregate)
		}

		// Track running total for this service+metric combination
		totalKey := data.Service + "|" + data.MetricName
		serviceTotals[totalKey] += sum
	}

	// PHASE 3: Add service totals for each metric type
	// serviceTotals contains keys like "AWS Certificate Manager|unblended_cost" with total values
	for totalKey, total := range serviceTotals {
		parts := strings.Split(totalKey, "|")
		service := parts[0]    // "AWS Certificate Manager"
		metricName := parts[1] // "unblended_cost"

		// Assign the total to the correct service and metric field
		// Example: aggregates.AWSCertificateManager.UnblendedCost["total"] = 2632.58
		switch service {
		case "AWS Certificate Manager":
			rs.assignServiceTotal(&aggregates.AWSCertificateManager, metricName, total)
		case "Amazon Managed Streaming for Apache Kafka":
			rs.assignServiceTotal(&aggregates.AmazonManagedStreamingForApacheKafka, metricName, total)
		case "EC2 - Other":
			rs.assignServiceTotal(&aggregates.EC2Other, metricName, total)
		}
	}

	return aggregates
}

// assignToServiceMetric assigns a cost aggregate to the correct metric field
func (rs *ReportService) assignToServiceMetric(service *types.ServiceCostAggregates, metricName, usageType string, aggregate types.CostAggregate) {
	switch metricName {
	case "unblended_cost":
		service.UnblendedCost[usageType] = aggregate
	case "blended_cost":
		service.BlendedCost[usageType] = aggregate
	case "amortized_cost":
		service.AmortizedCost[usageType] = aggregate
	case "net_amortized_cost":
		service.NetAmortizedCost[usageType] = aggregate
	case "net_unblended_cost":
		service.NetUnblendedCost[usageType] = aggregate
	}
}

// assignServiceTotal assigns a service total to the correct metric field
func (rs *ReportService) assignServiceTotal(service *types.ServiceCostAggregates, metricName string, total float64) {
	switch metricName {
	case "unblended_cost":
		service.UnblendedCost["total"] = total
	case "blended_cost":
		service.BlendedCost["total"] = total
	case "amortized_cost":
		service.AmortizedCost["total"] = total
	case "net_amortized_cost":
		service.NetAmortizedCost["total"] = total
	case "net_unblended_cost":
		service.NetUnblendedCost["total"] = total
	}
}

func (rs *ReportService) flattenCosts(region types.DiscoveredRegion) types.ProcessedRegionCosts {
	var processedCosts []types.ProcessedCost

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

			var costBreakdown types.ProcessedCostBreakdown

			if metric, exists := group.Metrics["UnblendedCost"]; exists && metric.Amount != nil {
				if costFloat, err := strconv.ParseFloat(aws.ToString(metric.Amount), 64); err == nil {
					costBreakdown.UnblendedCost = costFloat
				}
			}
			if metric, exists := group.Metrics["BlendedCost"]; exists && metric.Amount != nil {
				if costFloat, err := strconv.ParseFloat(aws.ToString(metric.Amount), 64); err == nil {
					costBreakdown.BlendedCost = costFloat
				}
			}
			if metric, exists := group.Metrics["AmortizedCost"]; exists && metric.Amount != nil {
				if costFloat, err := strconv.ParseFloat(aws.ToString(metric.Amount), 64); err == nil {
					costBreakdown.AmortizedCost = costFloat
				}
			}
			if metric, exists := group.Metrics["NetAmortizedCost"]; exists && metric.Amount != nil {
				if costFloat, err := strconv.ParseFloat(aws.ToString(metric.Amount), 64); err == nil {
					costBreakdown.NetAmortizedCost = costFloat
				}
			}
			if metric, exists := group.Metrics["NetUnblendedCost"]; exists && metric.Amount != nil {
				if costFloat, err := strconv.ParseFloat(aws.ToString(metric.Amount), 64); err == nil {
					costBreakdown.NetUnblendedCost = costFloat
				}
			}

			processedCosts = append(processedCosts, types.ProcessedCost{
				Start:     start,
				End:       end,
				Service:   service,
				UsageType: lineItem,
				Values:    costBreakdown,
			})
		}
	}

	return types.ProcessedRegionCosts{
		Metadata: region.Costs.CostMetadata,
		Results:  processedCosts,
	}
}

func (rs *ReportService) flattenMetrics(cluster types.DiscoveredCluster) types.ProcessedClusterMetrics {
	var processedMetrics []types.ProcessedMetric

	period := cluster.ClusterMetrics.MetricMetadata.Period
	// Iterate through each metric result
	for _, result := range cluster.ClusterMetrics.Results {
		label := result.Label
		if label == nil {
			continue
		}

		// Handle case where there are no timestamps/values (empty arrays)
		if len(result.Timestamps) == 0 || len(result.Values) == 0 {
			// Add a single entry with empty start/end and null value
			processedMetrics = append(processedMetrics, types.ProcessedMetric{
				Start: "",
				End:   "",
				Label: *label,
				Value: nil,
			})
			continue
		}

		// Iterate through timestamps and values (they should be paired)
		for i, timestamp := range result.Timestamps {
			// Ensure we don't go out of bounds for values
			if i >= len(result.Values) {
				break
			}

			// Calculate start and end times
			// start == timestamp
			// end == timestamp + (period - 1 second)
			startTime := timestamp
			endTime := timestamp.Add(time.Duration(period-1) * time.Second)

			// Convert to strings
			startStr := startTime.Format("2006-01-02T15:04:05Z")
			endStr := endTime.Format("2006-01-02T15:04:05Z")
			value := result.Values[i]

			processedMetrics = append(processedMetrics, types.ProcessedMetric{
				Start: startStr,
				End:   endStr,
				Label: *label,
				Value: &value,
			})
		}
	}

	return types.ProcessedClusterMetrics{
		Metrics:  processedMetrics,
		Metadata: cluster.ClusterMetrics.MetricMetadata,
	}
}

// calculateMetricsAggregates calculates avg, max, min aggregates for each metric type
func (rs *ReportService) calculateMetricsAggregates(metrics []types.ProcessedMetric) map[string]types.MetricAggregate {
	// Group metrics by label (metric type)
	metricsByLabel := make(map[string][]float64)

	for _, metric := range metrics {
		if metric.Value != nil {
			// Remove "Cluster Aggregate - " prefix if present
			cleanLabel := strings.TrimPrefix(metric.Label, "Cluster Aggregate - ")
			metricsByLabel[cleanLabel] = append(metricsByLabel[cleanLabel], *metric.Value)
		}
	}

	// Calculate aggregates for each metric type
	aggregates := make(map[string]types.MetricAggregate)

	for label, values := range metricsByLabel {
		if len(values) == 0 {
			continue
		}

		// Calculate min, max, sum for average
		min := values[0]
		max := values[0]
		sum := 0.0

		for _, value := range values {
			if value < min {
				min = value
			}
			if value > max {
				max = value
			}
			sum += value
		}

		avg := sum / float64(len(values))

		aggregates[label] = types.MetricAggregate{
			Average: &avg,
			Maximum: &max,
			Minimum: &min,
		}
	}

	return aggregates
}
