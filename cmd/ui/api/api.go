package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/cmd/ui/frontend"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/labstack/echo/v4"
	"github.com/zclconf/go-cty/cty"
)

type TerraformFiles struct {
	MainTf      string `json:"main_tf"`
	ProvidersTf string `json:"providers_tf"`
	VersionsTf  string `json:"versions_tf"`
	VariablesTf string `json:"variables_tf"`
	OutputsTf   string `json:"outputs_tf"`
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
	// Generate main.tf
	mainTf := ui.generateMainTf()

	// Generate providers.tf
	providersTf := ui.generateProvidersTf()

	// Generate versions.tf
	versionsTf := ui.generateVersionsTf()

	// Generate variables.tf
	variablesTf := ui.generateVariablesTf()

	// Generate outputs.tf
	outputsTf := ui.generateOutputsTf()

	terraformFiles := TerraformFiles{
		MainTf:      mainTf,
		ProvidersTf: providersTf,
		VersionsTf:  versionsTf,
		VariablesTf: variablesTf,
		OutputsTf:   outputsTf,
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

// generateMainTf generates the main.tf file content using HCL v2
func (ui *UI) generateMainTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Random string resource for suffixes
	randomStringBlock := rootBody.AppendNewBlock("resource", []string{"random_string", "suffix"})
	randomStringBody := randomStringBlock.Body()
	randomStringBody.SetAttributeValue("length", cty.NumberIntVal(4))
	randomStringBody.SetAttributeValue("special", cty.BoolVal(false))
	randomStringBody.SetAttributeValue("numeric", cty.BoolVal(false))
	randomStringBody.SetAttributeValue("upper", cty.BoolVal(false))

	// Confluent Environment
	environmentBlock := rootBody.AppendNewBlock("resource", []string{"confluent_environment", "environment"})
	environmentBody := environmentBlock.Body()
	environmentBody.SetAttributeValue("display_name", cty.StringVal("var.confluent_cloud_environment_name"))

	streamGovernanceBlock := environmentBody.AppendNewBlock("stream_governance", nil)
	streamGovernanceBody := streamGovernanceBlock.Body()
	streamGovernanceBody.SetAttributeValue("package", cty.StringVal("ADVANCED"))

	// Confluent Kafka Cluster
	clusterBlock := rootBody.AppendNewBlock("resource", []string{"confluent_kafka_cluster", "cluster"})
	clusterBody := clusterBlock.Body()
	clusterBody.SetAttributeValue("display_name", cty.StringVal("var.confluent_cloud_cluster_name"))
	clusterBody.SetAttributeValue("availability", cty.StringVal("SINGLE_ZONE"))
	clusterBody.SetAttributeValue("cloud", cty.StringVal("var.confluent_cloud_provider"))
	clusterBody.SetAttributeValue("region", cty.StringVal("var.confluent_cloud_region"))

	dedicatedBlock := clusterBody.AppendNewBlock("dedicated", nil)
	dedicatedBody := dedicatedBlock.Body()
	dedicatedBody.SetAttributeValue("cku", cty.NumberIntVal(1))

	environmentRefBlock := clusterBody.AppendNewBlock("environment", nil)
	environmentRefBody := environmentRefBlock.Body()
	environmentRefBody.SetAttributeValue("id", cty.StringVal("confluent_environment.environment.id"))

	// Schema Registry data source
	schemaRegistryDataBlock := rootBody.AppendNewBlock("data", []string{"confluent_schema_registry_cluster", "schema_registry"})
	schemaRegistryDataBody := schemaRegistryDataBlock.Body()
	environmentDataBlock := schemaRegistryDataBody.AppendNewBlock("environment", nil)
	environmentDataBody := environmentDataBlock.Body()
	environmentDataBody.SetAttributeValue("id", cty.StringVal("confluent_environment.environment.id"))
	schemaRegistryDataBody.SetAttributeValue("depends_on", cty.ListVal([]cty.Value{cty.StringVal("confluent_api_key.app-manager-kafka-api-key")}))

	// Service Account
	serviceAccountBlock := rootBody.AppendNewBlock("resource", []string{"confluent_service_account", "app-manager"})
	serviceAccountBody := serviceAccountBlock.Body()
	serviceAccountBody.SetAttributeValue("display_name", cty.StringVal("app-manager-${random_string.suffix.result}"))
	serviceAccountBody.SetAttributeValue("description", cty.StringVal("Service account to manage the ${var.confluent_cloud_environment_name} environment."))

	// Role Bindings
	roleBinding1Block := rootBody.AppendNewBlock("resource", []string{"confluent_role_binding", "subject-resource-owner"})
	roleBinding1Body := roleBinding1Block.Body()
	roleBinding1Body.SetAttributeValue("principal", cty.StringVal("User:${confluent_service_account.app-manager.id}"))
	roleBinding1Body.SetAttributeValue("role_name", cty.StringVal("ResourceOwner"))
	roleBinding1Body.SetAttributeValue("crn_pattern", cty.StringVal("${data.confluent_schema_registry_cluster.schema_registry.resource_name}/subject=*"))

	roleBinding2Block := rootBody.AppendNewBlock("resource", []string{"confluent_role_binding", "app-manager-kafka-cluster-admin"})
	roleBinding2Body := roleBinding2Block.Body()
	roleBinding2Body.SetAttributeValue("principal", cty.StringVal("User:${confluent_service_account.app-manager.id}"))
	roleBinding2Body.SetAttributeValue("role_name", cty.StringVal("CloudClusterAdmin"))
	roleBinding2Body.SetAttributeValue("crn_pattern", cty.StringVal("confluent_kafka_cluster.cluster.rbac_crn"))

	roleBinding3Block := rootBody.AppendNewBlock("resource", []string{"confluent_role_binding", "app-manager-kafka-data-steward"})
	roleBinding3Body := roleBinding3Block.Body()
	roleBinding3Body.SetAttributeValue("principal", cty.StringVal("User:${confluent_service_account.app-manager.id}"))
	roleBinding3Body.SetAttributeValue("role_name", cty.StringVal("DataSteward"))
	roleBinding3Body.SetAttributeValue("crn_pattern", cty.StringVal("confluent_environment.environment.resource_name"))

	// Kafka ACLs
	acl1Block := rootBody.AppendNewBlock("resource", []string{"confluent_kafka_acl", "app-manager-create-on-cluster"})
	acl1Body := acl1Block.Body()
	kafkaCluster1Block := acl1Body.AppendNewBlock("kafka_cluster", nil)
	kafkaCluster1Body := kafkaCluster1Block.Body()
	kafkaCluster1Body.SetAttributeValue("id", cty.StringVal("confluent_kafka_cluster.cluster.id"))
	acl1Body.SetAttributeValue("resource_type", cty.StringVal("CLUSTER"))
	acl1Body.SetAttributeValue("resource_name", cty.StringVal("kafka-cluster"))
	acl1Body.SetAttributeValue("pattern_type", cty.StringVal("LITERAL"))
	acl1Body.SetAttributeValue("principal", cty.StringVal("User:${confluent_service_account.app-manager.id}"))
	acl1Body.SetAttributeValue("host", cty.StringVal("*"))
	acl1Body.SetAttributeValue("operation", cty.StringVal("CREATE"))
	acl1Body.SetAttributeValue("permission", cty.StringVal("ALLOW"))
	acl1Body.SetAttributeValue("rest_endpoint", cty.StringVal("confluent_kafka_cluster.cluster.rest_endpoint"))
	credentials1Block := acl1Body.AppendNewBlock("credentials", nil)
	credentials1Body := credentials1Block.Body()
	credentials1Body.SetAttributeValue("key", cty.StringVal("confluent_api_key.app-manager-kafka-api-key.id"))
	credentials1Body.SetAttributeValue("secret", cty.StringVal("confluent_api_key.app-manager-kafka-api-key.secret"))

	acl2Block := rootBody.AppendNewBlock("resource", []string{"confluent_kafka_acl", "app-manager-describe-on-cluster"})
	acl2Body := acl2Block.Body()
	kafkaCluster2Block := acl2Body.AppendNewBlock("kafka_cluster", nil)
	kafkaCluster2Body := kafkaCluster2Block.Body()
	kafkaCluster2Body.SetAttributeValue("id", cty.StringVal("confluent_kafka_cluster.cluster.id"))
	acl2Body.SetAttributeValue("resource_type", cty.StringVal("CLUSTER"))
	acl2Body.SetAttributeValue("resource_name", cty.StringVal("kafka-cluster"))
	acl2Body.SetAttributeValue("pattern_type", cty.StringVal("LITERAL"))
	acl2Body.SetAttributeValue("principal", cty.StringVal("User:${confluent_service_account.app-manager.id}"))
	acl2Body.SetAttributeValue("host", cty.StringVal("*"))
	acl2Body.SetAttributeValue("operation", cty.StringVal("DESCRIBE"))
	acl2Body.SetAttributeValue("permission", cty.StringVal("ALLOW"))
	acl2Body.SetAttributeValue("rest_endpoint", cty.StringVal("confluent_kafka_cluster.cluster.rest_endpoint"))
	credentials2Block := acl2Body.AppendNewBlock("credentials", nil)
	credentials2Body := credentials2Block.Body()
	credentials2Body.SetAttributeValue("key", cty.StringVal("confluent_api_key.app-manager-kafka-api-key.id"))
	credentials2Body.SetAttributeValue("secret", cty.StringVal("confluent_api_key.app-manager-kafka-api-key.secret"))

	acl3Block := rootBody.AppendNewBlock("resource", []string{"confluent_kafka_acl", "app-manager-read-all-consumer-groups"})
	acl3Body := acl3Block.Body()
	kafkaCluster3Block := acl3Body.AppendNewBlock("kafka_cluster", nil)
	kafkaCluster3Body := kafkaCluster3Block.Body()
	kafkaCluster3Body.SetAttributeValue("id", cty.StringVal("confluent_kafka_cluster.cluster.id"))
	acl3Body.SetAttributeValue("resource_type", cty.StringVal("GROUP"))
	acl3Body.SetAttributeValue("resource_name", cty.StringVal("*"))
	acl3Body.SetAttributeValue("pattern_type", cty.StringVal("PREFIXED"))
	acl3Body.SetAttributeValue("principal", cty.StringVal("User:${confluent_service_account.app-manager.id}"))
	acl3Body.SetAttributeValue("host", cty.StringVal("*"))
	acl3Body.SetAttributeValue("operation", cty.StringVal("READ"))
	acl3Body.SetAttributeValue("permission", cty.StringVal("ALLOW"))
	acl3Body.SetAttributeValue("rest_endpoint", cty.StringVal("confluent_kafka_cluster.cluster.rest_endpoint"))
	credentials3Block := acl3Body.AppendNewBlock("credentials", nil)
	credentials3Body := credentials3Block.Body()
	credentials3Body.SetAttributeValue("key", cty.StringVal("confluent_api_key.app-manager-kafka-api-key.id"))
	credentials3Body.SetAttributeValue("secret", cty.StringVal("confluent_api_key.app-manager-kafka-api-key.secret"))

	// API Keys
	apiKey1Block := rootBody.AppendNewBlock("resource", []string{"confluent_api_key", "env-manager-schema-registry-api-key"})
	apiKey1Body := apiKey1Block.Body()
	apiKey1Body.SetAttributeValue("display_name", cty.StringVal("env-manager-schema-registry-api-key-${random_string.suffix.result}"))
	apiKey1Body.SetAttributeValue("description", cty.StringVal("Schema Registry API Key that is owned by the ${var.confluent_cloud_environment_name} environment."))
	owner1Block := apiKey1Body.AppendNewBlock("owner", nil)
	owner1Body := owner1Block.Body()
	owner1Body.SetAttributeValue("id", cty.StringVal("confluent_service_account.app-manager.id"))
	owner1Body.SetAttributeValue("api_version", cty.StringVal("confluent_service_account.app-manager.api_version"))
	owner1Body.SetAttributeValue("kind", cty.StringVal("confluent_service_account.app-manager.kind"))
	managedResource1Block := apiKey1Body.AppendNewBlock("managed_resource", nil)
	managedResource1Body := managedResource1Block.Body()
	managedResource1Body.SetAttributeValue("id", cty.StringVal("data.confluent_schema_registry_cluster.schema_registry.id"))
	managedResource1Body.SetAttributeValue("api_version", cty.StringVal("data.confluent_schema_registry_cluster.schema_registry.api_version"))
	managedResource1Body.SetAttributeValue("kind", cty.StringVal("data.confluent_schema_registry_cluster.schema_registry.kind"))
	environmentApiKey1Block := managedResource1Body.AppendNewBlock("environment", nil)
	environmentApiKey1Body := environmentApiKey1Block.Body()
	environmentApiKey1Body.SetAttributeValue("id", cty.StringVal("confluent_environment.environment.id"))

	apiKey2Block := rootBody.AppendNewBlock("resource", []string{"confluent_api_key", "app-manager-kafka-api-key"})
	apiKey2Body := apiKey2Block.Body()
	apiKey2Body.SetAttributeValue("display_name", cty.StringVal("app-manager-kafka-api-key-${random_string.suffix.result}"))
	apiKey2Body.SetAttributeValue("description", cty.StringVal("Kafka API Key that has been created by the ${var.confluent_cloud_environment_name} environment."))
	owner2Block := apiKey2Body.AppendNewBlock("owner", nil)
	owner2Body := owner2Block.Body()
	owner2Body.SetAttributeValue("id", cty.StringVal("confluent_service_account.app-manager.id"))
	owner2Body.SetAttributeValue("api_version", cty.StringVal("confluent_service_account.app-manager.api_version"))
	owner2Body.SetAttributeValue("kind", cty.StringVal("confluent_service_account.app-manager.kind"))
	managedResource2Block := apiKey2Body.AppendNewBlock("managed_resource", nil)
	managedResource2Body := managedResource2Block.Body()
	managedResource2Body.SetAttributeValue("id", cty.StringVal("confluent_kafka_cluster.cluster.id"))
	managedResource2Body.SetAttributeValue("api_version", cty.StringVal("confluent_kafka_cluster.cluster.api_version"))
	managedResource2Body.SetAttributeValue("kind", cty.StringVal("confluent_kafka_cluster.cluster.kind"))
	environmentApiKey2Block := managedResource2Body.AppendNewBlock("environment", nil)
	environmentApiKey2Body := environmentApiKey2Block.Body()
	environmentApiKey2Body.SetAttributeValue("id", cty.StringVal("confluent_environment.environment.id"))
	apiKey2Body.SetAttributeValue("depends_on", cty.ListVal([]cty.Value{cty.StringVal("confluent_role_binding.app-manager-kafka-cluster-admin")}))

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
	confluentProviderBlock := requiredProvidersBody.AppendNewBlock("confluent", nil)
	confluentProviderBody := confluentProviderBlock.Body()
	confluentProviderBody.SetAttributeValue("source", cty.StringVal("confluentinc/confluent"))
	confluentProviderBody.SetAttributeValue("version", cty.StringVal("2.23.0"))

	// Random provider
	randomProviderBlock := requiredProvidersBody.AppendNewBlock("random", nil)
	randomProviderBody := randomProviderBlock.Body()
	randomProviderBody.SetAttributeValue("source", cty.StringVal("hashicorp/random"))
	randomProviderBody.SetAttributeValue("version", cty.StringVal("3.7.2"))

	// Provider block
	providerBlock := rootBody.AppendNewBlock("provider", []string{"confluent"})
	providerBody := providerBlock.Body()
	providerBody.SetAttributeValue("cloud_api_key", cty.StringVal("var.confluent_cloud_api_key"))
	providerBody.SetAttributeValue("cloud_api_secret", cty.StringVal("var.confluent_cloud_api_secret"))

	return string(f.Bytes())
}

// generateVersionsTf generates the versions.tf file content using HCL v2
func (ui *UI) generateVersionsTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Terraform block
	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	// Required providers block
	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	// Confluent provider
	confluentProviderBlock := requiredProvidersBody.AppendNewBlock("confluent", nil)
	confluentProviderBody := confluentProviderBlock.Body()
	confluentProviderBody.SetAttributeValue("source", cty.StringVal("confluentinc/confluent"))
	confluentProviderBody.SetAttributeValue("version", cty.StringVal("2.23.0"))

	// Random provider
	randomProviderBlock := requiredProvidersBody.AppendNewBlock("random", nil)
	randomProviderBody := randomProviderBlock.Body()
	randomProviderBody.SetAttributeValue("source", cty.StringVal("hashicorp/random"))
	randomProviderBody.SetAttributeValue("version", cty.StringVal("3.7.2"))

	return string(f.Bytes())
}

// generateVariablesTf generates the variables.tf file content using HCL v2
func (ui *UI) generateVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Define all variables - matching confluent_cloud module
	variables := []struct {
		name        string
		description string
		sensitive   bool
	}{
		{"confluent_cloud_api_key", "", true},
		{"confluent_cloud_api_secret", "", true},
		{"confluent_cloud_region", "", false},
		{"confluent_cloud_provider", "", false},
		{"confluent_cloud_environment_name", "", false},
		{"confluent_cloud_cluster_name", "", false},
	}

	for _, v := range variables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeValue("type", cty.StringVal("string"))
		if v.description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.description))
		}
		if v.sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
	}

	return string(f.Bytes())
}

// generateOutputsTf generates the outputs.tf file content using HCL v2
func (ui *UI) generateOutputsTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Define all outputs - actual resource values
	outputs := []struct {
		name      string
		value     string
		sensitive bool
	}{
		{"confluent_cloud_cluster_rest_endpoint", "confluent_kafka_cluster.cluster.rest_endpoint", false},
		{"confluent_cloud_cluster_id", "confluent_kafka_cluster.cluster.id", false},
		{"confluent_cloud_cluster_api_key", "confluent_api_key.app-manager-kafka-api-key.id", true},
		{"confluent_cloud_cluster_api_key_secret", "confluent_api_key.app-manager-kafka-api-key.secret", true},
		{"confluent_cloud_cluster_bootstrap_endpoint", "confluent_kafka_cluster.cluster.bootstrap_endpoint", false},
		{"confluent_cloud_environment_id", "confluent_environment.environment.id", false},
		{"confluent_cloud_schema_registry_endpoint", "data.confluent_schema_registry_cluster.schema_registry.http_endpoint", false},
		{"confluent_cloud_schema_registry_api_key", "confluent_api_key.env-manager-schema-registry-api-key.id", true},
		{"confluent_cloud_schema_registry_api_key_secret", "confluent_api_key.env-manager-schema-registry-api-key.secret", true},
	}

	for _, o := range outputs {
		outputBlock := rootBody.AppendNewBlock("output", []string{o.name})
		outputBody := outputBlock.Body()
		outputBody.SetAttributeValue("value", cty.StringVal(o.value))
		if o.sensitive {
			outputBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
	}

	return string(f.Bytes())
}
