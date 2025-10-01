package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/cmd/ui/frontend"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
	"github.com/labstack/echo/v4"
)

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState
	FilterRegionCosts(processedState types.ProcessedState, regionName string, startTime, endTime *time.Time) (*types.ProcessedRegionCosts, error)
	FilterMetrics(processedState types.ProcessedState, regionName, clusterName string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error)
}

type UICmdOpts struct {
	Port string
}

type UI struct {
	reportService ReportService

	port        string
	cachedState *types.State // Cache the uploaded state for metrics filtering
}

func NewUI(reportService ReportService, opts UICmdOpts) *UI {
	return &UI{
		port:          opts.Port,
		reportService: reportService,
		cachedState:   nil,
	}
}

func (ui *UI) Run() error {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

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
	fullURL := fmt.Sprintf("http://%s", serverAddr)
	fmt.Printf("\nkcp ui is available at %s\n", color.New(color.FgGreen).Sprint(fullURL))

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
	region := c.Param("region")
	cluster := c.Param("cluster")

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
	filteredMetrics, err := ui.reportService.FilterMetrics(processedState, region, cluster, startTime, endTime)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{
			"error":   "Cluster not found",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, filteredMetrics)
}

func (ui *UI) handleGetCosts(c echo.Context) error {
	region := c.Param("region")

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
	regionCosts, err := ui.reportService.FilterRegionCosts(processedState, region, startTime, endTime)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{
			"error":   "Region not found",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, regionCosts)
}

func (ui *UI) handleGetCosts(c echo.Context) error {
	region := c.Param("region")

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

func (ui *UI) handleGetCosts(c echo.Context) error {
	region := c.Param("region")

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
	regionCosts, err := ui.reportService.FilterRegionCosts(processedState, region, startTime, endTime)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{
			"error":   "Region not found",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, regionCosts)
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
