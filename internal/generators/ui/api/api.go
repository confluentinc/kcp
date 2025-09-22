package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/internal/generators/ui/frontend"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/labstack/echo/v4"
)

type ReportService interface {
	ProcessCosts(region types.DiscoveredRegion) types.ProcessedRegionCosts
	ProcessMetrics(cluster types.DiscoveredCluster) types.ProcessedClusterMetrics
}

type UI struct {
	port          string
	reportService ReportService
}

func NewUI(port string, reportService ReportService) *UI {
	return &UI{
		port:          port,
		reportService: reportService,
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

	processedRegions := []types.ProcessedRegion{}

	// Process each region: flatten costs and metrics for frontend consumption
	for _, region := range state.Regions {
		// Flatten cost data from nested AWS Cost Explorer format
		processedCosts := ui.reportService.ProcessCosts(region)

		// Process each cluster's metrics
		processedClusters := []types.ProcessedCluster{}
		for _, cluster := range region.Clusters {
			// Flatten metrics data from nested CloudWatch format
			processedMetrics := ui.reportService.ProcessMetrics(cluster)

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

	return c.JSON(http.StatusOK, processedState)
}
