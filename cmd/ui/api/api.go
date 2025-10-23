package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/cmd/ui/frontend"
	"github.com/confluentinc/kcp/internal/services/hcl"
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
	reportService            ReportService
	targetInfraHCLService    hcl.TargetInfraHCLService
	migrationInfraHCLService hcl.MigrationInfraHCLService

	port        string
	cachedState *types.State // Cache the uploaded state for metrics filtering
}

func NewUI(reportService ReportService, targetInfraHCLService hcl.TargetInfraHCLService, migrationInfraHCLService hcl.MigrationInfraHCLService, opts UICmdOpts) *UI {
	return &UI{
		reportService:            reportService,
		targetInfraHCLService:    targetInfraHCLService,
		migrationInfraHCLService: migrationInfraHCLService,

		port:        opts.Port,
		cachedState: nil,
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

	e.POST("/assets/target", ui.handleTargetClusterAssets)
	e.POST("/assets/migration", ui.handleMigrationAssets)

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

func (ui *UI) handleTargetClusterAssets(c echo.Context) error {
	var req types.TargetClusterWizardRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	// Validate required fields based on what user needs
	if !req.NeedsEnvironment && !req.NeedsCluster {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid configuration",
			"message": "At least an environment or cluster must be configured",
		})
	}

	if req.NeedsEnvironment {
		if req.EnvironmentName == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Missing required fields",
				"message": "environmentName is required when creating a new environment",
			})
		}
		// When creating environment, cluster info is also required (based on wizard flow)
		if req.ClusterName == "" || req.ClusterType == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Missing required fields",
				"message": "clusterName and clusterType are required when creating a new environment",
			})
		}
	}

	if req.NeedsCluster && !req.NeedsEnvironment {
		if req.ClusterName == "" || req.ClusterType == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Missing required fields",
				"message": "clusterName and clusterType are required when creating a new cluster",
			})
		}
	}

	terraformFiles, err := ui.targetInfraHCLService.GenerateTerraformFiles(req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to generate Terraform files",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusCreated, terraformFiles)
}

func (ui *UI) handleMigrationAssets(c echo.Context) error {
	var req types.MigrationWizardRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	if req.AuthenticationMethod == "" || req.TargetClusterType == "" || req.TargetEnvironmentId == "" || req.TargetClusterId == "" || req.TargetRestEndpoint == "" || req.MskClusterId == "" || req.MskSaslScramBootstrapServers == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid configuration",
			"message": "authenticationMethod, targetClusterType, targetEnvironmentId, targetClusterId, targetRestEndpoint, mskClusterId, and mskSaslScramBootstrapServers are required",
		})
	}

	terraformFiles, err := ui.migrationInfraHCLService.GenerateTerraformFiles(req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to generate Terraform files",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusCreated, terraformFiles)
}
