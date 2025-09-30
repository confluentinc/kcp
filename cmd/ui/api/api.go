package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/confluentinc/kcp/cmd/ui/frontend"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/labstack/echo/v4"
)

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState
}

type UICmdOpts struct {
	Port string
}

type UI struct {
	port          string
	reportService ReportService
	cachedState   *types.State // Cache the uploaded state for metrics filtering
}

func NewUI(reportService ReportService, opts UICmdOpts) *UI {
	return &UI{
		port:          opts.Port,
		reportService: reportService,
		cachedState:   nil,
	}
}

func (ui *UI) Run() error {
	fmt.Println("Running UI...")

	e := echo.New()
	e.HideBanner = true

	frontend.RegisterHandlers(e)

	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"status":    "healthy",
			"service":   "kcp-ui",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	e.POST("/upload-state", ui.handleUploadState)
	e.GET("/metrics/:region/:cluster", ui.handleGetMetrics)
	e.GET("/costs/:region", ui.handleGetCosts)

	serverAddr := fmt.Sprintf("localhost:%s", ui.port)
	fmt.Printf("Starting UI server on %s\n", serverAddr)
	e.Logger.Fatal(e.Start(serverAddr))

	return nil
}

func (ui *UI) handleUploadState(c echo.Context) error {
	var state types.State

	if err := c.Bind(&state); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	// Cache the state for metrics filtering
	ui.cachedState = &state

	processedState := ui.reportService.ProcessState(state)

	return c.JSON(http.StatusOK, processedState)
}

func (ui *UI) handleGetMetrics(c echo.Context) error {
	// Extract path parameters
	region := c.Param("region")
	cluster := c.Param("cluster")

	// Extract query parameters
	startDate := c.QueryParam("startDate")
	endDate := c.QueryParam("endDate")

	// Check if we have cached state data
	if ui.cachedState == nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": "Please upload state data via POST /state first",
		})
	}

	// Parse date filters if provided
	var startTime, endTime *time.Time
	if startDate != "" {
		if parsed, err := time.Parse(time.RFC3339, startDate); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Invalid start date format",
				"message": "Start date must be in RFC3339 format (e.g., 2025-09-01T00:00:00Z)",
			})
		} else {
			startTime = &parsed
		}
	}
	if endDate != "" {
		if parsed, err := time.Parse(time.RFC3339, endDate); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Invalid end date format",
				"message": "End date must be in RFC3339 format (e.g., 2025-09-27T23:59:59Z)",
			})
		} else {
			endTime = &parsed
		}
	}

	// Process the full state to get structured data
	processedState := ui.reportService.ProcessState(*ui.cachedState)

	// Filter by region and cluster
	filteredMetrics, err := ui.filterMetrics(processedState, region, cluster, startTime, endTime)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{
			"error":   "Cluster not found",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, filteredMetrics)
}

func (ui *UI) handleGetCosts(c echo.Context) error {
	// Extract path parameter
	region := c.Param("region")

	// Extract query parameters
	startDate := c.QueryParam("startDate")
	endDate := c.QueryParam("endDate")

	// Check if we have cached state data
	if ui.cachedState == nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": "Please upload state data via POST /upload-state first",
		})
	}

	// Parse date filters if provided
	var startTime, endTime *time.Time
	if startDate != "" {
		if parsed, err := time.Parse(time.RFC3339, startDate); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Invalid start date format",
				"message": "Start date must be in RFC3339 format (e.g., 2025-09-01T00:00:00Z)",
			})
		} else {
			startTime = &parsed
		}
	}
	if endDate != "" {
		if parsed, err := time.Parse(time.RFC3339, endDate); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Invalid end date format",
				"message": "End date must be in RFC3339 format (e.g., 2025-09-27T23:59:59Z)",
			})
		} else {
			endTime = &parsed
		}
	}

	// Process the full state to get structured data
	processedState := ui.reportService.ProcessState(*ui.cachedState)

	// Filter costs by region
	regionCosts, err := ui.filterRegionCosts(processedState, region, startTime, endTime)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{
			"error":   "Region not found",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, regionCosts)
}

// filterMetrics filters the processed state by region, cluster, and date range
func (ui *UI) filterMetrics(processedState types.ProcessedState, regionName, clusterName string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error) {
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
	aggregates := calculateMetricsAggregates(filteredMetrics)

	return &types.ProcessedClusterMetrics{
		Metadata:   targetCluster.ClusterMetrics.Metadata,
		Metrics:    filteredMetrics,
		Aggregates: aggregates,
	}, nil
}

// calculateMetricsAggregates calculates avg, max, min aggregates for each metric type
func calculateMetricsAggregates(metrics []types.ProcessedMetric) map[string]types.MetricAggregate {
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

// filterRegionCosts filters the processed state to return cost data for a specific region
func (ui *UI) filterRegionCosts(processedState types.ProcessedState, regionName string, startTime, endTime *time.Time) (*types.ProcessedRegionCosts, error) {
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
	aggregates := ui.calculateCostAggregates(filteredCosts)

	return &types.ProcessedRegionCosts{
		Metadata:   regionCosts.Metadata,
		Results:    filteredCosts,
		Aggregates: aggregates,
	}, nil
}

// calculateCostAggregates takes raw cost data and calculates statistics for each unique combination
// of service + metric type + usage type, then organizes it into the final nested structure
func (ui *UI) calculateCostAggregates(costs []types.ProcessedCost) types.ProcessedAggregates {
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
	var groupedData []MetricData            // Final list of all unique combinations
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
				groupedData = append(groupedData, *newData)
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
			ui.assignToServiceMetric(&aggregates.AWSCertificateManager, data.MetricName, data.UsageType, costAggregate)
		case "Amazon Managed Streaming for Apache Kafka":
			ui.assignToServiceMetric(&aggregates.AmazonManagedStreamingForApacheKafka, data.MetricName, data.UsageType, costAggregate)
		case "EC2 - Other":
			ui.assignToServiceMetric(&aggregates.EC2Other, data.MetricName, data.UsageType, costAggregate)
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
			ui.assignServiceTotal(&aggregates.AWSCertificateManager, metricName, total)
		case "Amazon Managed Streaming for Apache Kafka":
			ui.assignServiceTotal(&aggregates.AmazonManagedStreamingForApacheKafka, metricName, total)
		case "EC2 - Other":
			ui.assignServiceTotal(&aggregates.EC2Other, metricName, total)
		}
	}

	return aggregates
}

// assignToServiceMetric assigns a cost aggregate to the correct metric field
func (ui *UI) assignToServiceMetric(service *types.ServiceCostAggregates, metricName, usageType string, aggregate types.CostAggregate) {
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
func (ui *UI) assignServiceTotal(service *types.ServiceCostAggregates, metricName string, total float64) {
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
