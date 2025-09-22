package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/internal/generators/ui/frontend"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/labstack/echo/v4"
)

type UI struct {
	port string
}

func StartAPI(port string) *UI {
	fmt.Println("Starting UI...")
	return &UI{port: port}
}

func (ui *UI) Run() error {
	fmt.Println("Running UI...")

	e := echo.New()
	e.HideBanner = true

	frontend.RegisterHandlers(e)

	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"status":    "healthy",
			"service":   "kcp-ui",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	e.POST("/state", ui.handleState)

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

	reportService := report.NewReportService()

	// Initialize slice to hold the processed regions with transformed cost and metrics data
	processedRegions := []types.ProcessedDiscoveredRegion{}

	// Process each region in the state data
	for _, region := range state.Regions {
		// Transform the raw AWS Cost Explorer results into flattened ProcessedRegionCosts
		// This converts complex nested AWS cost data into simple, flat structures
		processedCosts := reportService.ProcessCosts(region)

		// Create the processed cost information structure that combines:
		// - Original cost metadata (time ranges, granularity, etc.)
		// - Newly processed/flattened cost data
		processedCostInformation := types.ProcessedCostInformation{
			CostMetadata:         region.Costs.CostMetadata,
			ProcessedRegionCosts: processedCosts,
		}

		// Process each cluster's metrics data
		processedClusters := []types.ProcessDiscoveredCluster{}
		for _, cluster := range region.Clusters {
			// Transform the raw CloudWatch metrics into flattened ProcessedClusterMetrics
			// This converts complex nested AWS metrics data into simple, flat structures
			processedMetrics := reportService.ProcessMetrics(cluster)

			// Build the processed cluster with the simplified structure
			processedClusters = append(processedClusters, types.ProcessDiscoveredCluster{
				ClusterName: cluster.Name,     // Use cluster name
				ClusterArn:  cluster.Arn,      // Use cluster ARN
				Metrics:     processedMetrics, // Use processed metrics data
			})
		}

		// Build the processed region with the same structure as the original,
		// but with transformed cost and metrics data that's easier to consume
		processedRegions = append(processedRegions, types.ProcessedDiscoveredRegion{
			Name:           region.Name,              // Keep original region name
			Configurations: region.Configurations,    // Keep original MSK configurations
			Costs:          processedCostInformation, // Use processed cost data
			Clusters:       processedClusters,        // Use processed cluster information with flattened metrics
		})
	}

	// Create the final processed discovery response with:
	// - Processed regions (with flattened cost and metrics data)
	// - Original build info and timestamp from the input
	processedDiscovery := types.ProcessedDiscovery{
		Regions:      processedRegions,
		KcpBuildInfo: state.KcpBuildInfo, // Preserve original build information
		Timestamp:    state.Timestamp,    // Preserve original timestamp
	}

	// Return successful response with the processed discovery data
	// The frontend can now easily consume the flattened cost and metrics information
	return c.JSON(http.StatusOK, map[string]any{
		"status":  "success",
		"message": "Discovery data processed successfully",
		"result":  processedDiscovery,
	})
}
