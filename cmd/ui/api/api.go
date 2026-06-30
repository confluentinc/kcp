package api

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/confluentinc/kcp/cmd/ui/frontend"
	"github.com/confluentinc/kcp/internal/services/hcl"
	"github.com/confluentinc/kcp/internal/services/hcl/hclrequests"
	"github.com/confluentinc/kcp/internal/services/hcl/hcltypes"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
	"github.com/labstack/echo/v4"
)

type ReportService interface {
	ProcessState(state types.State) report.ProcessedState
	FilterRegionCosts(processedState report.ProcessedState, regionName string, startTime, endTime *time.Time) (*report.ProcessedRegionCosts, error)
	FilterMetrics(processedState report.ProcessedState, regionName, clusterName string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error)
	FilterClusterMetrics(processedState report.ProcessedState, clusterID string, sourceType string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error)
	FilterConnectMetrics(processedState report.ProcessedState, clusterID string, sourceType string, startTime, endTime *time.Time) (*types.ConnectClusterMetrics, error)
}

type UICmdOpts struct {
	Port      string
	StateFile string
}

type UI struct {
	reportService              ReportService
	targetInfraHCLService      hcl.TargetInfraGenerator
	migrationInfraHCLService   hcl.MigrationInfraGenerator
	migrationScriptsHCLService hcl.MigrationScriptsGenerator

	port        string
	states      map[string]*types.State // Session-based state storage (key: sessionId)
	statesMutex sync.RWMutex            // Protects concurrent access to states map
}

func NewUI(reportService ReportService, targetInfraHCLService hcl.TargetInfraGenerator, migrationInfraHCLService hcl.MigrationInfraGenerator, migrationScriptsHCLService hcl.MigrationScriptsGenerator, opts UICmdOpts) (*UI, error) {
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
		state, err := types.NewStateFromFile(opts.StateFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load state file %q: %w", opts.StateFile, err)
		}
		ui.states["default"] = state
		slog.Info("Pre-loaded state file", "path", opts.StateFile)

		hasSources := (state.MSKSources != nil && len(state.MSKSources.Regions) > 0) ||
			(state.OSKSources != nil && len(state.OSKSources.Clusters) > 0)
		hasSchemaRegistries := state.SchemaRegistries != nil &&
			(len(state.SchemaRegistries.ConfluentSchemaRegistry) > 0 || len(state.SchemaRegistries.AWSGlue) > 0)

		if !hasSources && hasSchemaRegistries {
			slog.Warn("No cluster sources found — run kcp discover (MSK) or kcp scan clusters (Apache Kafka) to populate", "path", opts.StateFile)
		} else if !hasSources && !hasSchemaRegistries {
			return nil, fmt.Errorf("state file %q contains no sources or schema registries — run kcp discover (MSK) or kcp scan clusters (Apache Kafka) to populate it", opts.StateFile)
		}
	}

	return ui, nil
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
	e.GET("/state", ui.handleGetState)

	e.GET("/metrics/:region/:cluster", ui.handleGetMetrics)
	e.GET("/metrics/osk/:clusterId", ui.handleGetOSKMetrics)
	// Connect is Kafka-distribution agnostic — one route serves both MSK and OSK.
	// The literal "connect" first segment avoids shadowing by the static "osk" node
	// and the "/metrics/:region/:cluster" param route.
	e.GET("/metrics/connect/:sourceType", ui.handleGetConnectMetrics)
	e.GET("/costs/:region", ui.handleGetCosts)

	e.POST("/upload-state", ui.handleUploadState)
	e.POST("/assets/migration", ui.handleMigrationAssets)
	e.POST("/assets/target", ui.handleTargetClusterAssets)
	e.POST("/assets/migration-scripts/acls", ui.handleMigrateAclsAssets)
	e.POST("/assets/migration-scripts/connectors", ui.handleMigrateConnectorsAssets)
	e.POST("/assets/migration-scripts/topics", ui.handleMigrateTopicsAssets)
	e.POST("/assets/migration-scripts/schemas", ui.handleMigrateSchemasAssets)
	e.POST("/assets/migration-scripts/glue-schemas", ui.handleMigrateGlueSchemasAssets)

	serverAddr := fmt.Sprintf("localhost:%s", ui.port)
	fullURL := fmt.Sprintf("http://%s", serverAddr)
	fmt.Printf("\nkcp ui is available at %s\n", color.New(color.FgGreen).Sprint(fullURL))

	e.Logger.Fatal(e.Start(serverAddr))

	return nil
}

func parseDateRange(c echo.Context) (*time.Time, *time.Time, error) {
	var startTime, endTime *time.Time
	if s := c.QueryParam("startDate"); s != "" {
		parsed, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid start date format: must be RFC3339 (e.g., 2025-09-01T00:00:00Z)")
		}
		startTime = &parsed
	}
	if s := c.QueryParam("endDate"); s != "" {
		parsed, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid end date format: must be RFC3339 (e.g., 2025-09-27T23:59:59Z)")
		}
		endTime = &parsed
	}
	return startTime, endTime, nil
}

func (ui *UI) handleGetState(c echo.Context) error {
	sessionId := c.QueryParam("sessionId")
	if sessionId == "" {
		sessionId = "default"
	}
	ui.statesMutex.Lock()
	state, exists := ui.states[sessionId]
	// Fall back to "default" session if specific session not found
	if !exists && sessionId != "default" {
		state, exists = ui.states["default"]
		if exists {
			// Copy default state to the requesting session so subsequent
			// requests (uploads, asset generation) work with this session ID
			ui.states[sessionId] = state
		}
	}
	ui.statesMutex.Unlock()
	if !exists {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "No state loaded"})
	}
	processedState := ui.reportService.ProcessState(*state)
	return c.JSON(http.StatusOK, processedState)
}

func (ui *UI) handleGetMetrics(c echo.Context) error {
	region := c.Param("region")
	cluster := c.Param("cluster")

	state, err := ui.getStateBySession(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": err.Error(),
		})
	}

	startTime, endTime, err := parseDateRange(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid date format",
			"message": err.Error(),
		})
	}

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

func (ui *UI) handleGetOSKMetrics(c echo.Context) error {
	clusterId := c.Param("clusterId")

	state, err := ui.getStateBySession(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": err.Error(),
		})
	}

	if state.OSKSources == nil {
		return c.JSON(http.StatusNotFound, map[string]any{
			"error": "No Apache Kafka sources in state",
		})
	}

	startTime, endTime, err := parseDateRange(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid date format",
			"message": err.Error(),
		})
	}

	processedState := ui.reportService.ProcessState(*state)

	filteredMetrics, err := ui.reportService.FilterClusterMetrics(processedState, clusterId, "osk", startTime, endTime)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{
			"error":   "Cluster not found or no metrics available",
			"message": err.Error(),
		})
	}

	if filteredMetrics.Metrics == nil {
		// An empty filtered result is ambiguous: the cluster either never had metrics
		// collected, or it has metrics but none fall in the selected date range. Only
		// the former warrants the "run a scan" hint. When a date filter was applied,
		// re-probe without it (reusing the same lookup) to tell them apart; an
		// out-of-range window then falls through to a normal empty 200 so the UI shows
		// an empty chart rather than a misleading error. FilterClusterMetrics is shared
		// with the `kcp report metrics` CLI, so the distinction is made here in the
		// handler rather than by changing the filter's contract.
		neverCollected := true
		if startTime != nil || endTime != nil {
			if unfiltered, uErr := ui.reportService.FilterClusterMetrics(processedState, clusterId, "osk", nil, nil); uErr == nil && unfiltered.Metrics != nil {
				neverCollected = false
			}
		}
		if neverCollected {
			return c.JSON(http.StatusNotFound, map[string]any{
				"error":   "No metrics available for this cluster",
				"message": "Run 'kcp scan clusters --source-type apache-kafka --metrics jolokia' or '--metrics prometheus' to collect metrics",
			})
		}
	}

	return c.JSON(http.StatusOK, filteredMetrics)
}

// handleGetConnectMetrics serves self-managed Connect metrics for either an MSK or OSK
// cluster. sourceType is an explicit path param ({msk, osk}); clusterId is a query param
// (OSK cluster id, or an MSK ARN URL-encoded by the client). The filter searches only the
// named source set, so a cluster id never resolves across source types.
func (ui *UI) handleGetConnectMetrics(c echo.Context) error {
	state, err := ui.getStateBySession(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": err.Error(),
		})
	}

	sourceType := c.Param("sourceType")
	if sourceType != "msk" && sourceType != "osk" {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid source type",
			"message": fmt.Sprintf("source type %q is not supported (must be 'msk' or 'osk')", sourceType),
		})
	}

	clusterId := c.QueryParam("clusterId")
	if clusterId == "" {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Missing cluster identifier",
			"message": "clusterId query parameter is required",
		})
	}

	startTime, endTime, err := parseDateRange(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid date format",
			"message": err.Error(),
		})
	}

	processedState := ui.reportService.ProcessState(*state)

	filteredMetrics, err := ui.reportService.FilterConnectMetrics(processedState, clusterId, sourceType, startTime, endTime)
	if err != nil {
		// "Never collected" is the only case that warrants the scan-guidance hint.
		// A cluster that HAS metrics but whose selected date range excludes them all
		// is not an error — it falls through to a normal (empty) 200 below, so the
		// user sees an empty chart rather than a misleading "run a scan" message.
		if errors.Is(err, report.ErrNoConnectMetricsCollected) {
			return c.JSON(http.StatusNotFound, map[string]any{
				"error":   "No Connect metrics available for this cluster",
				"message": "Run 'kcp scan self-managed-connectors --metrics jolokia' or '--metrics prometheus' to collect Connect metrics",
			})
		}
		return c.JSON(http.StatusNotFound, map[string]any{
			"error":   "Cluster not found or no Connect metrics available",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, filteredMetrics)
}

func (ui *UI) handleGetCosts(c echo.Context) error {
	region := c.Param("region")

	state, err := ui.getStateBySession(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "No state data available",
			"message": err.Error(),
		})
	}

	startTime, endTime, err := parseDateRange(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid date format",
			"message": err.Error(),
		})
	}

	processedState := ui.reportService.ProcessState(*state)

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

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Failed to read request body",
			"message": err.Error(),
		})
	}

	state, err := types.NewStateFromBytes(body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "State file could not be loaded",
			"message": err.Error(),
		})
	}

	// Store state in map using session ID as key
	ui.statesMutex.Lock()
	ui.states[sessionId] = state
	ui.statesMutex.Unlock()

	processedState := ui.reportService.ProcessState(*state)

	return c.JSON(http.StatusOK, processedState)
}

func (ui *UI) handleMigrationAssets(c echo.Context) error {
	var req hclrequests.MigrationWizardRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	// Block external outbound cluster linking for dedicated clusters
	if !req.HasPublicEndpoints && !req.UseJumpClusters && req.TargetClusterType == "dedicated" {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Unsupported configuration",
			"message": "External outbound cluster linking (Type 2/3) is not supported for dedicated clusters. Please use jump clusters (Type 4, 5, or 6) for private networking, or Type 1 (Cluster Link) if your MSK brokers are publicly accessible.",
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

func validateClusterLinkRequest(req hclrequests.MigrationWizardRequest) error {
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

func validatePrivateLinkRequest(req hclrequests.MigrationWizardRequest) error {
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

func validatePrivateClusterLinkRequest(req hclrequests.MigrationWizardRequest) error {
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

	if len(missingFields) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missingFields, ", "))
	}

	return nil
}

func (ui *UI) handleTargetClusterAssets(c echo.Context) error {
	// Default PreventDestroy to true before binding. If the JSON request includes
	// "prevent_destroy": false, the binding will override this. If the field is
	// omitted from the request, this default of true is preserved.
	req := hclrequests.TargetClusterWizardRequest{
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
	var req hclrequests.MigrateAclsRequest
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

	// Look up cluster ACLs based on source type
	var allAcls []types.Acls
	switch req.SourceType {
	case "osk":
		oskCluster, err := state.GetOSKClusterByID(req.ClusterId)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]any{
				"error":   "Cluster not found",
				"message": fmt.Sprintf("Apache Kafka cluster '%s' not found: %v", req.ClusterId, err),
			})
		}
		allAcls = oskCluster.KafkaAdminClientInformation.Acls
	default: // "msk" or empty (backward compat)
		cluster, err := state.GetClusterByArn(req.ClusterId)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]any{
				"error":   "Cluster not found",
				"message": fmt.Sprintf("MSK cluster '%s' not found: %v", req.ClusterId, err),
			})
		}
		allAcls = cluster.KafkaAdminClientInformation.Acls
	}

	selectedPrincipalsSet := make(map[string]bool)
	for _, p := range req.SelectedPrincipals {
		selectedPrincipalsSet[p] = true
	}

	aclsByPrincipal := make(map[string][]types.Acls)
	for _, acl := range allAcls {
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
	var req hclrequests.MirrorTopicsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	// Default mode is mirror for back-compat with pre-mode-flag clients;
	// the new wizard always sends Mode explicitly.
	if req.Mode == "" {
		req.Mode = hclrequests.MigrateTopicsModeMirror
	}

	// --mode new emits confluent_kafka_topic resources with partitions and
	// configs preserved from source — those live in state, not in the wizard
	// payload. Hydrate from state using the (source_type, cluster_id) the
	// wizard sends as hidden fields and the selected_topics name list as the
	// filter. Mirror mode doesn't need this (cluster link drives configs).
	if req.Mode == hclrequests.MigrateTopicsModeNew {
		if err := ui.hydrateTopicsFromState(c, &req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Failed to hydrate topic details for new mode",
				"message": err.Error(),
			})
		}
	}

	project, err := ui.migrationScriptsHCLService.GenerateMirrorTopicsFiles(req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to generate Terraform files",
			"message": err.Error(),
		})
	}

	// Flatten the single-folder per-topic layout back into the legacy
	// TerraformFiles shape so the existing wizard UI (which renders main.tf /
	// providers.tf / variables.tf) keeps working. Per-topic file output is
	// a CLI-only capability in this iteration — UI parity is deferred.
	terraformFiles := flattenMigrateTopicsProject(project)

	return c.JSON(http.StatusCreated, terraformFiles)
}

// hydrateTopicsFromState looks up the source cluster in the loaded state and
// populates req.Topics with full TopicDetails (partitions, configurations) for
// each name in req.SelectedTopics. New-mode HCL generation needs partition
// counts and config maps that the wizard's name-only selection can't carry.
func (ui *UI) hydrateTopicsFromState(c echo.Context, req *hclrequests.MirrorTopicsRequest) error {
	if req.ClusterId == "" || req.SourceType == "" {
		return fmt.Errorf("source_type and cluster_id are required for --mode new (wizard should send these as hidden fields)")
	}

	state, err := ui.getStateBySession(c)
	if err != nil {
		return fmt.Errorf("session state lookup: %w", err)
	}

	var details []types.TopicDetails
	switch req.SourceType {
	case "msk":
		cluster, err := state.GetClusterByArn(req.ClusterId)
		if err != nil {
			return fmt.Errorf("lookup MSK cluster %q: %w", req.ClusterId, err)
		}
		if cluster.KafkaAdminClientInformation.Topics != nil {
			details = cluster.KafkaAdminClientInformation.Topics.Details
		}
	case "osk":
		cluster, err := state.GetOSKClusterByID(req.ClusterId)
		if err != nil {
			return fmt.Errorf("lookup Apache Kafka cluster %q: %w", req.ClusterId, err)
		}
		if cluster.KafkaAdminClientInformation.Topics != nil {
			details = cluster.KafkaAdminClientInformation.Topics.Details
		}
	default:
		return fmt.Errorf("invalid source_type %q (must be 'msk' or 'osk')", req.SourceType)
	}

	byName := make(map[string]types.TopicDetails, len(details))
	for _, d := range details {
		byName[d.Name] = d
	}

	req.Topics = make([]types.TopicDetails, 0, len(req.SelectedTopics))
	for _, name := range req.SelectedTopics {
		d, ok := byName[name]
		if !ok {
			return fmt.Errorf("topic %q not found in state for cluster %q (state may be stale — re-run `kcp scan clusters`)", name, req.ClusterId)
		}
		req.Topics = append(req.Topics, d)
	}
	return nil
}

// flattenMigrateTopicsProject concatenates per-topic .tf files into a single
// main.tf string so the legacy UI wizard contract stays intact while the CLI
// uses the per-file layout.
func flattenMigrateTopicsProject(project hcltypes.MigrationScriptsTerraformProject) hcltypes.TerraformFiles {
	out := hcltypes.TerraformFiles{}
	if len(project.Folders) == 0 {
		return out
	}
	folder := project.Folders[0]
	out.ProvidersTf = folder.ProvidersTf
	out.VariablesTf = folder.VariablesTf

	names := make([]string, 0, len(folder.AdditionalFiles))
	for n := range folder.AdditionalFiles {
		names = append(names, n)
	}
	// Deterministic ordering so UI output is stable across requests.
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(folder.AdditionalFiles[n])
	}
	out.MainTf = b.String()
	return out
}

func (ui *UI) handleMigrateSchemasAssets(c echo.Context) error {
	var req hclrequests.MigrateSchemasRequest
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

func (ui *UI) handleMigrateGlueSchemasAssets(c echo.Context) error {
	var req hclrequests.MigrateGlueSchemasRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	migrationScriptsProject, err := ui.migrationScriptsHCLService.GenerateMigrateGlueSchemasFiles(req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to generate Glue schema migration project",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusCreated, migrationScriptsProject)
}
