package api

import (
	"fmt"
	"net/http"
	"strings"
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
	reportService              ReportService
	targetInfraHCLService      hcl.TargetInfraHCLService
	migrationInfraHCLService   hcl.MigrationInfraHCLService
	migrationScriptsHCLService hcl.MigrationScriptsHCLService

	port        string
	cachedState *types.State // Cache the uploaded state for metrics filtering
}

func NewUI(reportService ReportService, targetInfraHCLService hcl.TargetInfraHCLService, migrationInfraHCLService hcl.MigrationInfraHCLService, migrationScriptsHCLService hcl.MigrationScriptsHCLService, opts UICmdOpts) *UI {
	return &UI{
		reportService:              reportService,
		targetInfraHCLService:      targetInfraHCLService,
		migrationInfraHCLService:   migrationInfraHCLService,
		migrationScriptsHCLService: migrationScriptsHCLService,

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

func (ui *UI) handleMigrationAssets(c echo.Context) error {
	var req types.MigrationWizardRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	if req.HasPublicCcEndpoints {
		if err := validateClusterLinkRequest(req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Invalid request body",
				"message": err.Error(),
			})
		}
	} else {
		if err := validatePrivateLinkRequest(req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":   "Invalid request body",
				"message": err.Error(),
			})
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
	if req.MskSaslScramBootstrapServers == "" {
		missingFields = append(missingFields, "mskSaslScramBootstrapServers")
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
	if req.MskJumpClusterAuthType == "" {
		missingFields = append(missingFields, "mskJumpClusterAuthType")
	}
	if req.TargetClusterId == "" {
		missingFields = append(missingFields, "targetClusterId")
	}
	// This might be missing depending on the MskJumpClusterAuthType.
	// if req.MskSaslScramBootstrapServers == "" {
	// 	missingFields = append(missingFields, "mskSaslScramBootstrapServers")
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

	var conditionalErrors []string

	// Conditional validation based on UseExistingSubnets
	if req.ReuseExistingSubnets {
		if len(req.PrivateLinkExistingSubnetIds) == 0 {
			conditionalErrors = append(conditionalErrors, "privateLinkExistingSubnetIds is required when reuseExistingSubnets is true")
		}
	} else {
		if len(req.PrivateLinkNewSubnetsCidr) == 0 {
			conditionalErrors = append(conditionalErrors, "privateLinkNewSubnetsCidr is required when reuseExistingSubnets is false")
		}
	}

	var allErrors []string
	if len(missingFields) > 0 {
		allErrors = append(allErrors, fmt.Sprintf("missing required fields: %s", strings.Join(missingFields, ", ")))
	}
	if len(conditionalErrors) > 0 {
		allErrors = append(allErrors, conditionalErrors...)
	}

	if len(allErrors) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(allErrors, "; "))
	}

	return nil
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

	terraformFiles := ui.targetInfraHCLService.GenerateTerraformFiles(req)

	return c.JSON(http.StatusCreated, terraformFiles)
}

func (ui *UI) handleMigrateAclsAssets(c echo.Context) error {
	return c.JSON(http.StatusCreated, "todo")
}

func (ui *UI) handleMigrateConnectorsAssets(c echo.Context) error {
	return c.JSON(http.StatusCreated, "todo")
}

func (ui *UI) handleMigrateTopicsAssets(c echo.Context) error {
	var req types.MigrateTopicsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	terraformFiles, err := ui.migrationScriptsHCLService.GenerateMigrateTopicsFiles(req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{
			"error":   "Failed to generate Terraform files",
			"message": err.Error(),
		})
	}

	return c.JSON(http.StatusCreated, terraformFiles)
}

func (ui *UI) handleMigrateSchemasAssets(c echo.Context) error {
	// todo: validation ?
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

	// // print json of terraformFolders
	// json, err := json.Marshal(terraformFolders)
	// if err != nil {
	// 	return c.JSON(http.StatusInternalServerError, map[string]any{
	// 		"error":   "Failed to marshal Terraform folders",
	// 		"message": err.Error(),
	// 	})
	// }
	// fmt.Println(string(json))
	return c.JSON(http.StatusCreated, migrationScriptsProject)
}

/*
{
    "main.tf": "module \"cluster_link\" {\n  source = \"./cluster_link\"\n\n  confluent_cloud_api_key            = var.confluent_cloud_api_key\n  confluent_cloud_api_secret         = var.confluent_cloud_api_secret\n  msk_sasl_scram_username            = var.msk_sasl_scram_username\n  msk_sasl_scram_password            = var.msk_sasl_scram_password\n  confluent_cloud_cluster_api_key    = var.confluent_cloud_cluster_api_key\n  confluent_cloud_cluster_api_secret = var.confluent_cloud_cluster_api_secret\n}\n",
    "providers.tf": "terraform {\n  required_providers {\n    confluent = {\n      source  = \"confluentinc/confluent\"\n      version = \"2.50.0\"\n    }\n  }\n}\n\nprovider \"confluent\" {\n  cloud_api_key    = var.confluent_cloud_api_key\n  cloud_api_secret = var.confluent_cloud_api_secret\n}\n\n",
    "variables.tf": "variable \"confluent_cloud_api_key\" {\n  type        = string\n  description = \"Confluent Cloud API Key\"\n}\n\nvariable \"confluent_cloud_api_secret\" {\n  type        = string\n  description = \"Confluent Cloud API Secret\"\n  sensitive   = true\n}\n\nvariable \"msk_sasl_scram_username\" {\n  type        = string\n  description = \"MSK SASL SCRAM Username\"\n}\n\nvariable \"msk_sasl_scram_password\" {\n  type        = string\n  description = \"MSK SASL SCRAM Password\"\n  sensitive   = true\n}\n\nvariable \"confluent_cloud_cluster_api_key\" {\n  type        = string\n  description = \"Confluent Cloud cluster API key\"\n}\n\nvariable \"confluent_cloud_cluster_api_secret\" {\n  type        = string\n  description = \"Confluent Cloud cluster API secret\"\n  sensitive   = true\n}\n\n",
    "inputs.auto.tfvars": "",
    "modules": [
        {
            "name": "cluster_link",
            "main.tf": "locals {\n  basic_auth_credentials = base64encode(\"${var.confluent_cloud_cluster_api_key}:${var.confluent_cloud_cluster_api_secret}\")\n}\n\nresource \"null_resource\" \"confluent_cluster_link\" {\n  triggers = {\n    source_cluster_id      = \"3Db5QLSqSZieL3rJBUUegA\"\n    destination_cluster_id = \"esffsdsd\"\n    bootstrap_servers      = \"b-2-public.publicretentionenvclus.i3n4q4.c2.kafka.eu-west-3.amazonaws.com:9196,b-1-public.publicretentionenvclus.i3n4q4.c2.kafka.eu-west-3.amazonaws.com:9196,b-3-public.publicretentionenvclus.i3n4q4.c2.kafka.eu-west-3.amazonaws.com:9196\"\n  }\n\n  provisioner \"local-exec\" {\n    command = \u003c\u003c-EOT\n    curl --request POST \\\n  --url 'dfsdffd/kafka/v3/clusters/esffsdsd/links/?link_name=dsgdffd' \\\n  --header 'Authorization: Basic ${local.basic_auth_credentials}' \\\n  --header \"Content-Type: application/json\" \\\n  --data '{\n    \"source_cluster_id\": \"3Db5QLSqSZieL3rJBUUegA\",\n    \"configs\": [\n      {\n        \"name\": \"bootstrap.servers\",\n        \"value\": \"b-2-public.publicretentionenvclus.i3n4q4.c2.kafka.eu-west-3.amazonaws.com:9196,b-1-public.publicretentionenvclus.i3n4q4.c2.kafka.eu-west-3.amazonaws.com:9196,b-3-public.publicretentionenvclus.i3n4q4.c2.kafka.eu-west-3.amazonaws.com:9196\"\n      },\n      {\n        \"name\": \"link.mode\",\n        \"value\": \"DESTINATION\"\n      },\n      {\n        \"name\": \"security.protocol\",\n        \"value\": \"SASL_SSL\"\n      },\n      {\n        \"name\": \"sasl.mechanism\",\n        \"value\": \"SCRAM-SHA-512\"\n      },\n      {\n        \"name\": \"sasl.jaas.config\",\n        \"value\": \"org.apache.kafka.common.security.scram.ScramLoginModule required username=\\\"${var.msk_sasl_scram_username}\\\" password=\\\"${var.msk_sasl_scram_password}\\\";\"\n      }\n    ]\n  }'\n    EOT\n  }\n}\n\n",
            "variables.tf": "variable \"confluent_cloud_api_key\" {\n  type        = string\n  description = \"Confluent Cloud API Key\"\n}\n\nvariable \"confluent_cloud_api_secret\" {\n  type        = string\n  description = \"Confluent Cloud API Secret\"\n  sensitive   = true\n}\n\nvariable \"msk_sasl_scram_username\" {\n  type        = string\n  description = \"MSK SASL SCRAM Username\"\n}\n\nvariable \"msk_sasl_scram_password\" {\n  type        = string\n  description = \"MSK SASL SCRAM Password\"\n  sensitive   = true\n}\n\nvariable \"confluent_cloud_cluster_api_key\" {\n  type        = string\n  description = \"Confluent Cloud cluster API key\"\n}\n\nvariable \"confluent_cloud_cluster_api_secret\" {\n  type        = string\n  description = \"Confluent Cloud cluster API secret\"\n  sensitive   = true\n}\n\n",
            "outputs.tf": "",
            "versions.tf": "",
            "additional_files": null
        }
    ]
}
*/
