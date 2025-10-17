package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/cmd/ui/frontend"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/labstack/echo/v4"
	"github.com/zclconf/go-cty/cty"
)

type TerraformFiles struct {
	MainTf      string `json:"main_tf"`
	ProvidersTf string `json:"providers_tf"`
	VariablesTf string `json:"variables_tf"`
}

type WizardRequest struct {
	NeedsEnvironment bool   `json:"needsEnvironment"`
	EnvironmentName  string `json:"environmentName"`
	EnvironmentId    string `json:"environmentId"`
	NeedsCluster     bool   `json:"needsCluster"`
	ClusterName      string `json:"clusterName"`
	ClusterType      string `json:"clusterType"`
}

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
	e.POST("/assets", ui.handlePostAssets)

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

func (ui *UI) handlePostAssets(c echo.Context) error {
	// Bind the wizard JSON payload
	var req WizardRequest
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

	// Generate main.tf with the wizard data
	mainTf := ui.generateMainTf(req)

	// Generate providers.tf
	providersTf := ui.generateProvidersTf()

	// Generate variables.tf
	variablesTf := ui.generateVariablesTf()

	terraformFiles := TerraformFiles{
		MainTf:      mainTf,
		ProvidersTf: providersTf,
		VariablesTf: variablesTf,
	}

	return c.JSON(http.StatusCreated, terraformFiles)
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

// tokensForTemplate creates properly formatted tokens for a template string (string with ${} interpolations)
func tokensForTemplate(template string) hclwrite.Tokens {
	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte(template)},
		&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	}
}

// generateMainTf generates the main.tf file content using HCL v2
func (ui *UI) generateMainTf(req WizardRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Confluent Environment (create if needed, otherwise use data source)
	if req.NeedsEnvironment {
		environmentBlock := hclwrite.NewBlock("resource", []string{"confluent_environment", "environment"})
		environmentBlock.Body().SetAttributeValue("display_name", cty.StringVal(req.EnvironmentName))
		environmentBlock.Body().AppendNewline()

		streamGovernanceBlock := hclwrite.NewBlock("stream_governance", nil)
		streamGovernanceBlock.Body().SetAttributeValue("package", cty.StringVal("ADVANCED")) // TODO: We can ask the user if they want essentials or advanced?
		environmentBlock.Body().AppendBlock(streamGovernanceBlock)

		rootBody.AppendBlock(environmentBlock)
		rootBody.AppendNewline()
	} else {
		// Use existing environment via data source
		environmentDataBlock := hclwrite.NewBlock("data", []string{"confluent_environment", "environment"})
		environmentDataBlock.Body().SetAttributeValue("id", cty.StringVal(req.EnvironmentId))

		rootBody.AppendBlock(environmentDataBlock)
		rootBody.AppendNewline()
	}

	// Confluent Kafka Cluster (create if needed, otherwise use data source)
	if req.NeedsCluster || req.NeedsEnvironment {
		clusterBlock := hclwrite.NewBlock("resource", []string{"confluent_kafka_cluster", "cluster"})
		clusterBlock.Body().SetAttributeValue("display_name", cty.StringVal(req.ClusterName))
		clusterBlock.Body().SetAttributeValue("availability", cty.StringVal("SINGLE_ZONE"))
		clusterBlock.Body().SetAttributeValue("cloud", cty.StringVal("AWS"))
		// TODO: Keep a list of regions in the wizard or elsewhere for regions - May need to consider single-zone vs multi-zone vs enterprise vs dedicated availability.
		clusterBlock.Body().SetAttributeValue("region", cty.StringVal("us-east-1"))
		clusterBlock.Body().AppendNewline()

		if req.ClusterType == "dedicated" {
			dedicatedBlock := clusterBlock.Body().AppendNewBlock("dedicated", nil)
			dedicatedBlock.Body().SetAttributeValue("cku", cty.NumberIntVal(1))
		}

		clusterBlock.Body().AppendNewline()
		environmentRefBlock := hclwrite.NewBlock("environment", nil)
		if req.NeedsEnvironment {
			environmentRefBlock.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_environment.environment.id")}})
		} else {
			environmentRefBlock.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("data.confluent_environment.environment.id")}})
		}

		clusterBlock.Body().AppendBlock(environmentRefBlock)
		rootBody.AppendBlock(clusterBlock)
		rootBody.AppendNewline()
	}

	// Path not supported until we ask for the PNI question.
	// else {
	// 	// Use existing cluster via data source
	// 	clusterDataBlock := rootBody.AppendNewBlock("data", []string{"confluent_kafka_cluster", "cluster"})
	// 	clusterDataBody := clusterDataBlock.Body()
	// 	clusterDataBody.SetAttributeValue("id", cty.StringVal("var.confluent_cloud_cluster_id"))
	// 	environmentRefBlock := clusterDataBody.AppendNewBlock("environment", nil)
	// 	environmentRefBody := environmentRefBlock.Body()
	// 	environmentRefBody.SetAttributeValue("id", cty.StringVal("data.confluent_environment.environment.id"))
	// }

	// Schema Registry data source
	schemaRegistryDataBlock := hclwrite.NewBlock("data", []string{"confluent_schema_registry_cluster", "schema_registry"})
	environmentSRBlock := hclwrite.NewBlock("environment", nil)
	if req.NeedsEnvironment {
		environmentSRBlock.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_environment.environment.id")}})
	} else {
		environmentSRBlock.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("data.confluent_environment.environment.id")}})
	}
	schemaRegistryDataBlock.Body().AppendBlock(environmentSRBlock)
	schemaRegistryDataBlock.Body().AppendNewline()
	schemaRegistryDataBlock.Body().SetAttributeRaw("depends_on", hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_api_key.app-manager-kafka-api-key")},
		&hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")},
	})

	rootBody.AppendBlock(schemaRegistryDataBlock)
	rootBody.AppendNewline()

	// Service Account
	serviceAccountBlock := hclwrite.NewBlock("resource", []string{"confluent_service_account", "app-manager"})
	serviceAccountBlock.Body().SetAttributeValue("display_name", cty.StringVal("app-manager"))
	serviceAccountBlock.Body().SetAttributeValue("description", cty.StringVal(fmt.Sprintf("Service account to manage the %s environment.", req.EnvironmentName)))

	rootBody.AppendBlock(serviceAccountBlock)
	rootBody.AppendNewline()

	// Role Bindings
	roleBinding1Block := hclwrite.NewBlock("resource", []string{"confluent_role_binding", "subject-resource-owner"})
	roleBinding1Block.Body().SetAttributeRaw("principal", tokensForTemplate("User:${confluent_service_account.app-manager.id}"))
	roleBinding1Block.Body().SetAttributeValue("role_name", cty.StringVal("ResourceOwner"))
	roleBinding1Block.Body().SetAttributeRaw("crn_pattern", tokensForTemplate("${data.confluent_schema_registry_cluster.schema_registry.resource_name}/subject=*"))

	rootBody.AppendBlock(roleBinding1Block)
	rootBody.AppendNewline()

	roleBinding2Block := hclwrite.NewBlock("resource", []string{"confluent_role_binding", "app-manager-kafka-cluster-admin"})
	roleBinding2Block.Body().SetAttributeRaw("principal", tokensForTemplate("User:${confluent_service_account.app-manager.id}"))
	roleBinding2Block.Body().SetAttributeValue("role_name", cty.StringVal("CloudClusterAdmin"))
	roleBinding2Block.Body().SetAttributeRaw("crn_pattern", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.rbac_crn")}})

	rootBody.AppendBlock(roleBinding2Block)
	rootBody.AppendNewline()

	roleBinding3Block := hclwrite.NewBlock("resource", []string{"confluent_role_binding", "app-manager-kafka-data-steward"})
	roleBinding3Block.Body().SetAttributeRaw("principal", tokensForTemplate("User:${confluent_service_account.app-manager.id}"))
	roleBinding3Block.Body().SetAttributeValue("role_name", cty.StringVal("DataSteward"))
	if req.NeedsEnvironment {
		roleBinding3Block.Body().SetAttributeRaw("crn_pattern", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_environment.environment.resource_name")}})
	} else {
		roleBinding3Block.Body().SetAttributeRaw("crn_pattern", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("data.confluent_environment.environment.resource_name")}})
	}

	rootBody.AppendBlock(roleBinding3Block)
	rootBody.AppendNewline()

	// Kafka ACLs
	acl1Block := hclwrite.NewBlock("resource", []string{"confluent_kafka_acl", "app-manager-create-on-cluster"})
	kafkaCluster1Block := hclwrite.NewBlock("kafka_cluster", nil)
	kafkaCluster1Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.id")}})
	acl1Block.Body().AppendBlock(kafkaCluster1Block)
	acl1Block.Body().AppendNewline()

	acl1Block.Body().SetAttributeValue("resource_type", cty.StringVal("CLUSTER"))
	acl1Block.Body().SetAttributeValue("resource_name", cty.StringVal("kafka-cluster"))
	acl1Block.Body().SetAttributeValue("pattern_type", cty.StringVal("LITERAL"))
	acl1Block.Body().SetAttributeRaw("principal", tokensForTemplate("User:${confluent_service_account.app-manager.id}"))
	acl1Block.Body().SetAttributeValue("host", cty.StringVal("*"))
	acl1Block.Body().SetAttributeValue("operation", cty.StringVal("CREATE"))
	acl1Block.Body().SetAttributeValue("permission", cty.StringVal("ALLOW"))
	acl1Block.Body().SetAttributeRaw("rest_endpoint", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.rest_endpoint")}})
	acl1Block.Body().AppendNewline()

	credentials1Block := hclwrite.NewBlock("credentials", nil)
	credentials1Block.Body().SetAttributeRaw("key", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_api_key.app-manager-kafka-api-key.id")}})
	credentials1Block.Body().SetAttributeRaw("secret", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_api_key.app-manager-kafka-api-key.secret")}})
	acl1Block.Body().AppendBlock(credentials1Block)

	rootBody.AppendBlock(acl1Block)
	rootBody.AppendNewline()

	acl2Block := hclwrite.NewBlock("resource", []string{"confluent_kafka_acl", "app-manager-describe-on-cluster"})
	kafkaCluster2Block := hclwrite.NewBlock("kafka_cluster", nil)
	kafkaCluster2Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.id")}})
	acl2Block.Body().AppendBlock(kafkaCluster2Block)
	acl2Block.Body().AppendNewline()

	acl2Block.Body().SetAttributeValue("resource_type", cty.StringVal("CLUSTER"))
	acl2Block.Body().SetAttributeValue("resource_name", cty.StringVal("kafka-cluster"))
	acl2Block.Body().SetAttributeValue("pattern_type", cty.StringVal("LITERAL"))
	acl2Block.Body().SetAttributeRaw("principal", tokensForTemplate("User:${confluent_service_account.app-manager.id}"))
	acl2Block.Body().SetAttributeValue("host", cty.StringVal("*"))
	acl2Block.Body().SetAttributeValue("operation", cty.StringVal("DESCRIBE"))
	acl2Block.Body().SetAttributeValue("permission", cty.StringVal("ALLOW"))
	acl2Block.Body().SetAttributeRaw("rest_endpoint", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.rest_endpoint")}})
	acl2Block.Body().AppendNewline()

	credentials2Block := hclwrite.NewBlock("credentials", nil)
	credentials2Block.Body().SetAttributeRaw("key", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_api_key.app-manager-kafka-api-key.id")}})
	credentials2Block.Body().SetAttributeRaw("secret", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_api_key.app-manager-kafka-api-key.secret")}})
	acl2Block.Body().AppendBlock(credentials2Block)

	rootBody.AppendBlock(acl2Block)
	rootBody.AppendNewline()

	acl3Block := hclwrite.NewBlock("resource", []string{"confluent_kafka_acl", "app-manager-read-all-consumer-groups"})
	kafkaCluster3Block := hclwrite.NewBlock("kafka_cluster", nil)
	kafkaCluster3Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.id")}})
	acl3Block.Body().AppendBlock(kafkaCluster3Block)
	acl3Block.Body().AppendNewline()

	acl3Block.Body().SetAttributeValue("resource_type", cty.StringVal("GROUP"))
	acl3Block.Body().SetAttributeValue("resource_name", cty.StringVal("*"))
	acl3Block.Body().SetAttributeValue("pattern_type", cty.StringVal("PREFIXED"))
	acl3Block.Body().SetAttributeRaw("principal", tokensForTemplate("User:${confluent_service_account.app-manager.id}"))
	acl3Block.Body().SetAttributeValue("host", cty.StringVal("*"))
	acl3Block.Body().SetAttributeValue("operation", cty.StringVal("READ"))
	acl3Block.Body().SetAttributeValue("permission", cty.StringVal("ALLOW"))
	acl3Block.Body().SetAttributeRaw("rest_endpoint", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.rest_endpoint")}})
	acl3Block.Body().AppendNewline()

	credentials3Block := hclwrite.NewBlock("credentials", nil)
	credentials3Block.Body().SetAttributeRaw("key", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_api_key.app-manager-kafka-api-key.id")}})
	credentials3Block.Body().SetAttributeRaw("secret", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_api_key.app-manager-kafka-api-key.secret")}})
	acl3Block.Body().AppendBlock(credentials3Block)

	rootBody.AppendBlock(acl3Block)
	rootBody.AppendNewline()

	// API Keys
	apiKey1Block := hclwrite.NewBlock("resource", []string{"confluent_api_key", "env-manager-schema-registry-api-key"})
	apiKey1Block.Body().SetAttributeValue("display_name", cty.StringVal("env-manager-schema-registry-api-key"))
	apiKey1Block.Body().SetAttributeValue("description", cty.StringVal(fmt.Sprintf("Schema Registry API Key that is owned by the %s environment.", req.EnvironmentName)))
	apiKey1Block.Body().AppendNewline()

	owner1Block := hclwrite.NewBlock("owner", nil)
	owner1Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_service_account.app-manager.id")}})
	owner1Block.Body().SetAttributeRaw("api_version", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_service_account.app-manager.api_version")}})
	owner1Block.Body().SetAttributeRaw("kind", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_service_account.app-manager.kind")}})
	apiKey1Block.Body().AppendBlock(owner1Block)
	apiKey1Block.Body().AppendNewline()

	managedResource1Block := hclwrite.NewBlock("managed_resource", nil)
	managedResource1Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("data.confluent_schema_registry_cluster.schema_registry.id")}})
	managedResource1Block.Body().SetAttributeRaw("api_version", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("data.confluent_schema_registry_cluster.schema_registry.api_version")}})
	managedResource1Block.Body().SetAttributeRaw("kind", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("data.confluent_schema_registry_cluster.schema_registry.kind")}})

	environmentApiKey1Block := hclwrite.NewBlock("environment", nil)
	if req.NeedsEnvironment {
		environmentApiKey1Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_environment.environment.id")}})
	} else {
		environmentApiKey1Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("data.confluent_environment.environment.id")}})
	}
	managedResource1Block.Body().AppendBlock(environmentApiKey1Block)
	apiKey1Block.Body().AppendBlock(managedResource1Block)

	rootBody.AppendBlock(apiKey1Block)
	rootBody.AppendNewline()

	apiKey2Block := hclwrite.NewBlock("resource", []string{"confluent_api_key", "app-manager-kafka-api-key"})
	apiKey2Block.Body().SetAttributeValue("display_name", cty.StringVal("app-manager-kafka-api-key"))
	apiKey2Block.Body().SetAttributeValue("description", cty.StringVal(fmt.Sprintf("Kafka API Key that has been created by the %s environment.", req.EnvironmentName)))
	apiKey2Block.Body().AppendNewline()

	owner2Block := hclwrite.NewBlock("owner", nil)
	owner2Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_service_account.app-manager.id")}})
	owner2Block.Body().SetAttributeRaw("api_version", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_service_account.app-manager.api_version")}})
	owner2Block.Body().SetAttributeRaw("kind", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_service_account.app-manager.kind")}})
	apiKey2Block.Body().AppendBlock(owner2Block)
	apiKey2Block.Body().AppendNewline()

	managedResource2Block := hclwrite.NewBlock("managed_resource", nil)
	managedResource2Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.id")}})
	managedResource2Block.Body().SetAttributeRaw("api_version", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.api_version")}})
	managedResource2Block.Body().SetAttributeRaw("kind", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_kafka_cluster.cluster.kind")}})
	managedResource2Block.Body().AppendNewline()

	environmentApiKey2Block := hclwrite.NewBlock("environment", nil)
	if req.NeedsEnvironment {
		environmentApiKey2Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_environment.environment.id")}})
	} else {
		environmentApiKey2Block.Body().SetAttributeRaw("id", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("data.confluent_environment.environment.id")}})
	}
	managedResource2Block.Body().AppendBlock(environmentApiKey2Block)
	apiKey2Block.Body().AppendBlock(managedResource2Block)
	apiKey2Block.Body().AppendNewline()

	apiKey2Block.Body().SetAttributeRaw("depends_on", hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("confluent_role_binding.app-manager-kafka-cluster-admin")},
		&hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")},
	})

	rootBody.AppendBlock(apiKey2Block)
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// generateProvidersTf generates the providers.tf file content using HCL v2
func (ui *UI) generateProvidersTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Terraform block
	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	// Required providers block
	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	// Confluent provider
	requiredProvidersBody.SetAttributeValue("confluent", cty.ObjectVal(map[string]cty.Value{
		"source":  cty.StringVal("confluentinc/confluent"),
		"version": cty.StringVal("2.50.0"),
	}))

	rootBody.AppendNewline()

	// Provider block
	providerBlock := rootBody.AppendNewBlock("provider", []string{"confluent"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeRaw("cloud_api_key", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("var.confluent_cloud_api_key")}})
	providerBody.SetAttributeRaw("cloud_api_secret", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("var.confluent_cloud_api_secret")}})

	return string(f.Bytes())
}

// generateVariablesTf generates the variables.tf file content using HCL v2
func (ui *UI) generateVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Define base variables
	variables := []struct {
		name        string
		description string
		sensitive   bool
	}{
		{"confluent_cloud_api_key", "Confluent Cloud API Key", true},
		{"confluent_cloud_api_secret", "Confluent Cloud API Secret", true},
	}

	for _, v := range variables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeRaw("type", hclwrite.Tokens{&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("string")}})
		if v.description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.description))
		}
		if v.sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
	}

	return string(f.Bytes())
}
