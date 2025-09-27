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

	e.POST("/state", ui.handleState)
	e.GET("/metrics/:region/:cluster", ui.handleMetrics)

	serverAddr := fmt.Sprintf("localhost:%s", ui.port)
	fmt.Printf("Starting UI server on %s\n", serverAddr)
	e.Logger.Fatal(e.Start(serverAddr))

	return nil
}

func (ui *UI) handleState(c echo.Context) error {
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

func (ui *UI) handleMetrics(c echo.Context) error {
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

	// If no date filters, return all metrics
	if startTime == nil && endTime == nil {
		return &targetCluster.ClusterMetrics, nil
	}

	// Filter metrics by date range
	var filteredMetrics []types.ProcessedMetric
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

	return &types.ProcessedClusterMetrics{
		Metadata: targetCluster.ClusterMetrics.Metadata,
		Metrics:  filteredMetrics,
	}, nil
}
