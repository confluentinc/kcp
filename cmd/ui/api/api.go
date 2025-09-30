package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

	// write json to a file called myoutput.json
	jsonData, _ := json.Marshal(regionCosts)
	if err := os.WriteFile("myoutput.json", jsonData, 0644); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to write region costs to file",
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

// calculateCostAggregates calculates totals and aggregates for cost data in nested format
func calculateCostAggregates(costs []types.ProcessedCost) types.ProcessedAggregates {
	aggregates := types.ProcessedAggregates{}

	if len(costs) == 0 {
		return aggregates
	}

	// Initialize all service maps for all cost types
	aggregates.AWSCertificateManager.UnblendedCost = make(map[string]any)
	aggregates.AWSCertificateManager.BlendedCost = make(map[string]any)
	aggregates.AWSCertificateManager.AmortizedCost = make(map[string]any)
	aggregates.AWSCertificateManager.NetAmortizedCost = make(map[string]any)
	aggregates.AWSCertificateManager.NetUnblendedCost = make(map[string]any)
	aggregates.AWSCertificateManager.UsageQuantity = make(map[string]any)

	aggregates.AmazonManagedStreamingForApacheKafka.UnblendedCost = make(map[string]any)
	aggregates.AmazonManagedStreamingForApacheKafka.BlendedCost = make(map[string]any)
	aggregates.AmazonManagedStreamingForApacheKafka.AmortizedCost = make(map[string]any)
	aggregates.AmazonManagedStreamingForApacheKafka.NetAmortizedCost = make(map[string]any)
	aggregates.AmazonManagedStreamingForApacheKafka.NetUnblendedCost = make(map[string]any)
	aggregates.AmazonManagedStreamingForApacheKafka.UsageQuantity = make(map[string]any)

	aggregates.EC2Other.UnblendedCost = make(map[string]any)
	aggregates.EC2Other.BlendedCost = make(map[string]any)
	aggregates.EC2Other.AmortizedCost = make(map[string]any)
	aggregates.EC2Other.NetAmortizedCost = make(map[string]any)
	aggregates.EC2Other.NetUnblendedCost = make(map[string]any)
	aggregates.EC2Other.UsageQuantity = make(map[string]any)

	// Group costs by service, usage type, and metric type
	serviceMetricUsageData := make(map[string]map[string]map[string][]float64)

	for _, cost := range costs {
		service := cost.Service
		usageType := cost.UsageType

		// Initialize nested structure
		if serviceMetricUsageData[service] == nil {
			serviceMetricUsageData[service] = make(map[string]map[string][]float64)
		}

		// Process each cost metric
		metrics := map[string]float64{
			"unblended_cost":     cost.Values.UnblendedCost,
			"blended_cost":       cost.Values.BlendedCost,
			"amortized_cost":     cost.Values.AmortizedCost,
			"net_amortized_cost": cost.Values.NetAmortizedCost,
			"net_unblended_cost": cost.Values.NetUnblendedCost,
			"usage_quantity":     cost.Values.UsageQuantity,
		}

		for metricName, value := range metrics {
			if serviceMetricUsageData[service][metricName] == nil {
				serviceMetricUsageData[service][metricName] = make(map[string][]float64)
			}
			serviceMetricUsageData[service][metricName][usageType] = append(serviceMetricUsageData[service][metricName][usageType], value)
		}
	}

	// Process each service and populate the struct fields
	for service, metricData := range serviceMetricUsageData {
		for metricName, usageTypes := range metricData {
			serviceTotal := 0.0

			for usageType, values := range usageTypes {
				if len(values) == 0 {
					continue
				}

				// Calculate aggregates for this usage type
				sum := 0.0
				min := values[0]
				max := values[0]

				for _, value := range values {
					sum += value
					if value < min {
						min = value
					}
					if value > max {
						max = value
					}
				}

				avg := sum / float64(len(values))
				serviceTotal += sum

				costAggregate := types.CostAggregate{
					Sum:     &sum,
					Average: &avg,
					Maximum: &max,
					Minimum: &min,
				}

				// Assign to the correct service and metric field
				switch service {
				case "AWS Certificate Manager":
					assignToServiceMetric(&aggregates.AWSCertificateManager, metricName, usageType, costAggregate)
				case "Amazon Managed Streaming for Apache Kafka":
					assignToServiceMetric(&aggregates.AmazonManagedStreamingForApacheKafka, metricName, usageType, costAggregate)
				case "EC2 - Other":
					assignToServiceMetric(&aggregates.EC2Other, metricName, usageType, costAggregate)
				}
			}

			// Add service total for this metric
			switch service {
			case "AWS Certificate Manager":
				assignServiceTotal(&aggregates.AWSCertificateManager, metricName, serviceTotal)
			case "Amazon Managed Streaming for Apache Kafka":
				assignServiceTotal(&aggregates.AmazonManagedStreamingForApacheKafka, metricName, serviceTotal)
			case "EC2 - Other":
				assignServiceTotal(&aggregates.EC2Other, metricName, serviceTotal)
			}
		}
	}

	return aggregates
}

// assignToServiceMetric assigns a cost aggregate to the correct metric field
func assignToServiceMetric(service *types.ServiceCostAggregates, metricName, usageType string, aggregate types.CostAggregate) {
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
	case "usage_quantity":
		service.UsageQuantity[usageType] = aggregate
	}
}

// assignServiceTotal assigns a service total to the correct metric field
func assignServiceTotal(service *types.ServiceCostAggregates, metricName string, total float64) {
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
	case "usage_quantity":
		service.UsageQuantity["total"] = total
	}
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
	aggregates := calculateCostAggregates(filteredCosts)

	return &types.ProcessedRegionCosts{
		Metadata:   regionCosts.Metadata,
		Results:    filteredCosts,
		Aggregates: aggregates,
	}, nil
}
