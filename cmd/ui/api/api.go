package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
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
	Port      string
	StateFile string
}

type UI struct {
	reportService              ReportService
	targetInfraHCLService      hcl.TargetInfraHCLService
	migrationInfraHCLService   hcl.MigrationInfraHCLService
	migrationScriptsHCLService hcl.MigrationScriptsHCLService

	port        string
	states      map[string]*types.State // Session-based state storage (key: sessionId)
	statesMutex sync.RWMutex            // Protects concurrent access to states map
}

func NewUI(reportService ReportService, targetInfraHCLService hcl.TargetInfraHCLService, migrationInfraHCLService hcl.MigrationInfraHCLService, migrationScriptsHCLService hcl.MigrationScriptsHCLService, opts UICmdOpts) *UI {
	ui := &UI{
		reportService:              reportService,
		targetInfraHCLService:      targetInfraHCLService,
		migrationInfraHCLService:   migrationInfraHCLService,
		migrationScriptsHCLService: migrationScriptsHCLService,

		port:   opts.Port,
		states: make(map[string]*types.State),
	}

	// Pre-load state file if provided
	if opts.StateFile != "" {
		data, err := os.ReadFile(opts.StateFile)
		if err != nil {
			slog.Error("Failed to read state file", "path", opts.StateFile, "error", err)
		} else {
			var state types.State
			if err := json.Unmarshal(data, &state); err != nil {
				slog.Error("Failed to parse state file", "path", opts.StateFile, "error", err)
			} else {
				ui.states["default"] = &state
				slog.Info("Pre-loaded state file", "path", opts.StateFile)
			}
		}
	}

	return ui
}

// getStateBySession extracts the sessionId from the request and retrieves the corresponding state
func (ui *UI) getStateBySession(c echo.Context) (*types.State, error) {
	sessionId := c.QueryParam("sessionId")
	if sessionId == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	ui.statesMutex.RLock()
	state, exists := ui.states[sessionId]
	ui.statesMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no state found for session %s. Please upload state data first", sessionId)
	}

	return state, nil
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

	// Get pre-loaded state endpoint
	e.GET("/state", func(c echo.Context) error {
		sessionId := c.QueryParam("sessionId")
		if sessionId == "" {
			sessionId = "default"
		}
		ui.statesMutex.RLock()
		state, exists := ui.states[sessionId]
		// Fall back to "default" session if specific session not found
		if !exists && sessionId != "default" {
			state, exists = ui.states["default"]
			if exists {
				// Copy default state to the requesting session so subsequent
				// requests (uploads, asset generation) work with this session ID
				ui.statesMutex.RUnlock()
				ui.statesMutex.Lock()
				ui.states[sessionId] = state
				ui.statesMutex.Unlock()
			} else {
				ui.statesMutex.RUnlock()
			}
		} else {
			ui.statesMutex.RUnlock()
		}
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "No state loaded"})
		}
		processedState := ui.reportService.ProcessState(*state)
		return c.JSON(http.StatusOK, processedState)
	})

	e.GET("/metrics/:region/:cluster", ui.handleGetMetrics)
	e.GET("/costs/:region", ui.handleGetCosts)

	e.POST("/upload-state", ui.handleUploadState)
	e.POST("/assets/migration", ui.handleMigrationAssets)
	e.POST("/assets/target", ui.handleTargetClusterAssets)
	e.POST("/assets/migration-scripts/acls", ui.handleMigrateAclsAssets)
	e.POST("/assets/migration-scripts/connectors", ui.handleMigrateConnectorsAssets)
	e.POST("/assets/migration-scripts/topics", ui.handleMigrateTopicsAssets)
	e.POST("/assets/migration-scripts/schemas", ui.handleMigrateSchemasAssets)

	serverAddr := fmt.Sprintf("localhost:%s", ui.port)
	fullURL := fmt.Sprintf("http://%s", serverAddr)
	fmt.Printf("\nkcp ui is available at %s\n", color.New(color.FgGreen).Sprint(fullURL))

	e.Logger.Fatal(e.Start(serverAddr))

	return nil
}

func (ui *UI) handleGetMetrics(c echo.Context) error {
	region := c.Param("region")
	cluster := c.Param("cluster")

	startDate := c.QueryParam("startDate")
	endDate := c.QueryParam("endDate")

	// Get state by session ID
	state, err := ui.getStateBySession(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": err.Error(),
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
	processedState := ui.reportService.ProcessState(*state)

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

	// Get state by session ID
	state, err := ui.getStateBySession(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": err.Error(),
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
	processedState := ui.reportService.ProcessState(*state)

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

func (ui *UI) handleUploadState(c echo.Context) error {
	sessionId := c.QueryParam("sessionId")
	if sessionId == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "sessionId is required",
			"message": "Please provide a sessionId query parameter",
		})
	}

	var state types.State
	if err := c.Bind(&state); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	// Store state in map using session ID as key
	ui.statesMutex.Lock()
	ui.states[sessionId] = &state
	ui.statesMutex.Unlock()

	processedState := ui.reportService.ProcessState(state)

	return c.JSON(http.StatusOK, processedState)
}

func (ui *UI) handleMigrationAssets(c echo.Context) error {
	var req types.MigrationWizardRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	if req.HasPublicEndpoints {
		if err := validateClusterLinkRequest(req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Invalid request body",
				"message": err.Error(),
			})
		}
	} else {
		if req.UseJumpClusters {
			if err := validatePrivateLinkRequest(req); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]any{
					"error":   "Invalid request body",
					"message": err.Error(),
				})
			}
		} else {
			if err := validatePrivateClusterLinkRequest(req); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]any{
					"error":   "Invalid request body",
					"message": err.Error(),
				})
			}
		}
	}

	terraformModules := ui.migrationInfraHCLService.GenerateTerraformModules(req)

	return c.JSON(http.StatusCreated, terraformModules)
}

func validateClusterLinkRequest(req types.MigrationWizardRequest) error {
	var missingFields []string

	if req.TargetClusterId == "" {
		missingFields = append(missingFields, "targetClusterId")
	}
	if req.TargetRestEndpoint == "" {
		missingFields = append(missingFields, "targetRestEndpoint")
	}
	if req.ClusterLinkName == "" {
		missingFields = append(missingFields, "clusterLinkName")
	}
	if req.SourceSaslScramBootstrapServers == "" {
		missingFields = append(missingFields, "sourceSaslScramBootstrapServers")
	}

	if len(missingFields) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missingFields, ", "))
	}

	return nil
}

func validatePrivateLinkRequest(req types.MigrationWizardRequest) error {
	var missingFields []string

	// Check required fields
	if req.VpcId == "" {
		missingFields = append(missingFields, "vpcId")
	}
	if req.JumpClusterInstanceType == "" {
		missingFields = append(missingFields, "jumpClusterInstanceType")
	}
	if req.JumpClusterBrokerStorage <= 0 {
		missingFields = append(missingFields, "jumpClusterBrokerStorage")
	}
	if len(req.JumpClusterBrokerSubnetCidr) == 0 {
		missingFields = append(missingFields, "jumpClusterBrokerSubnetCidr")
	}
	if req.JumpClusterSetupHostSubnetCidr == "" {
		missingFields = append(missingFields, "jumpClusterSetupHostSubnetCidr")
	}
	if req.JumpClusterAuthType == "" {
		missingFields = append(missingFields, "jumpClusterAuthType")
	}
	if req.TargetClusterId == "" {
		missingFields = append(missingFields, "targetClusterId")
	}
	// This might be missing depending on the MskJumpClusterAuthType.
	// if req.MskSaslScramBootstrapServers == "" {
	// 	missingFields = append(missingFields, "sourceSaslScramBootstrapServers")
	// }
	if req.TargetRestEndpoint == "" {
		missingFields = append(missingFields, "targetRestEndpoint")
	}
	if req.TargetBootstrapEndpoint == "" {
		missingFields = append(missingFields, "targetBootstrapEndpoint")
	}
	if req.ClusterLinkName == "" {
		missingFields = append(missingFields, "clusterLinkName")
	}

	if len(missingFields) > 0 {
		return fmt.Errorf("invalid configuration: missing required fields: %s", strings.Join(missingFields, ", "))
	}

	return nil
}

func validatePrivateClusterLinkRequest(req types.MigrationWizardRequest) error {
	var missingFields []string

	if req.VpcId == "" {
		missingFields = append(missingFields, "vpcId")
	}
	if req.SourceRegion == "" {
		missingFields = append(missingFields, "sourceRegion")
	}
	if req.SourceClusterId == "" {
		missingFields = append(missingFields, "sourceClusterId")
	}
	if req.ExtOutboundSecurityGroupId == "" {
		missingFields = append(missingFields, "extOutboundSecurityGroupId")
	}
	if req.ExtOutboundSubnetId == "" {
		missingFields = append(missingFields, "extOutboundSubnetId")
	}
	if req.ExtOutboundBrokers == nil {
		missingFields = append(missingFields, "extOutboundBrokers")
	}
	if req.TargetEnvironmentId == "" {
		missingFields = append(missingFields, "targetEnvironmentId")
	}
	if req.TargetClusterId == "" {
		missingFields = append(missingFields, "targetClusterId")
	}
	if req.TargetRestEndpoint == "" {
		missingFields = append(missingFields, "targetRestEndpoint")
	}
	if req.ClusterLinkName == "" {
		missingFields = append(missingFields, "clusterLinkName")
	}

	var conditionalErrors []string

	if len(missingFields) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missingFields, ", "))
	}
	if len(conditionalErrors) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(conditionalErrors, "; "))
	}

	return nil
}

func (ui *UI) handleTargetClusterAssets(c echo.Context) error {
	// Default PreventDestroy to true before binding. If the JSON request includes
	// "prevent_destroy": false, the binding will override this. If the field is
	// omitted from the request, this default of true is preserved.
	req := types.TargetClusterWizardRequest{
		PreventDestroy: true,
	}
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

	// Apply defaults for dedicated cluster settings
	if req.ClusterType == "dedicated" {
		if req.ClusterAvailability == "" {
			req.ClusterAvailability = "SINGLE_ZONE"
		}
		if req.ClusterCku == 0 {
			req.ClusterCku = 1
		}
	}

	terraformFiles := ui.targetInfraHCLService.GenerateTerraformFiles(req)

	return c.JSON(http.StatusCreated, terraformFiles)
}

func (ui *UI) handleMigrateAclsAssets(c echo.Context) error {
	var req types.MigrateAclsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	// Get state by session ID
	state, err := ui.getStateBySession(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": err.Error(),
		})
	}

	var targetCluster *types.DiscoveredCluster
	if state.MSKSources != nil {
		for _, region := range state.MSKSources.Regions {
			if region.Name != req.MskRegion {
				continue
			}
			for i := range region.Clusters {
				if region.Clusters[i].Arn == req.MskClusterArn {
					targetCluster = &region.Clusters[i]
					break
				}
			}
		}
	}

	if targetCluster == nil {
		return c.JSON(http.StatusNotFound, map[string]any{
			"error":   "Cluster not found",
			"message": fmt.Sprintf("No cluster found with ARN %s in region %s", req.MskClusterArn, req.MskRegion),
		})
	}

	selectedPrincipalsSet := make(map[string]bool)
	for _, p := range req.SelectedPrincipals {
		selectedPrincipalsSet[p] = true
	}

	aclsByPrincipal := make(map[string][]types.Acls)
	for _, acl := range targetCluster.KafkaAdminClientInformation.Acls {
		if selectedPrincipalsSet[acl.Principal] {
			aclsByPrincipal[acl.Principal] = append(aclsByPrincipal[acl.Principal], acl)
		}
	}

	// Attach the filtered ACLs to the request
	req.AclsByPrincipal = aclsByPrincipal

	terraformFiles, err := ui.migrationScriptsHCLService.GenerateMigrateAclsFiles(req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to generate Terraform files",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusCreated, terraformFiles)
}

func (ui *UI) handleMigrateConnectorsAssets(c echo.Context) error {
	return c.JSON(http.StatusCreated, "todo")
}

func (ui *UI) handleMigrateTopicsAssets(c echo.Context) error {
	var req types.MirrorTopicsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	terraformFiles, err := ui.migrationScriptsHCLService.GenerateMirrorTopicsFiles(req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to generate Terraform files",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusCreated, terraformFiles)
}

func (ui *UI) handleMigrateSchemasAssets(c echo.Context) error {
	var req types.MigrateSchemasRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	migrationScriptsProject, err := ui.migrationScriptsHCLService.GenerateMigrateSchemasFiles(req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to generate Migration Scripts project",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusCreated, migrationScriptsProject)
}
