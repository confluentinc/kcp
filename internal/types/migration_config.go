package types

// MigrationConfig holds all domain configuration for a migration
// This is pure data with no behavior - just fields that get serialized
type MigrationConfig struct {
	MigrationId  string `json:"migration_id"`
	CurrentState string `json:"current_state"`

	// Gateway configuration
	GatewayNamespace     string `json:"gateway_namespace"`
	GatewayCrdName       string `json:"gateway_crd_name"`
	SourceName           string `json:"source_name"`
	DestinationName      string `json:"destination_name"`
	SourceRouteName      string `json:"source_route_name"`
	DestinationRouteName string `json:"destination_route_name"`
	KubeConfigPath       string `json:"kube_config_path"`

	// Cluster link configuration
	ClusterId           string   `json:"cluster_id"`
	ClusterRestEndpoint string   `json:"cluster_rest_endpoint"`
	ClusterLinkName     string   `json:"cluster_link_name"`
	Topics              []string `json:"topics"`
	AuthMode            string   `json:"auth_mode"`

	// Migration runtime data (populated during initialization)
	ClusterLinkTopics   []string          `json:"cluster_link_topics"`
	ClusterLinkConfigs  map[string]string `json:"cluster_link_configs"`
	GatewayOriginalYAML []byte            `json:"gateway_original_yaml"`

	// Cloud endpoints
	CCBootstrapEndpoint  string `json:"cc_bootstrap_endpoint"`
	LoadBalancerEndpoint string `json:"load_balancer_endpoint"`
}

// MigrationConfigOpts contains options for creating a new migration config
type MigrationConfigOpts struct {
	GatewayNamespace     string
	GatewayCrdName       string
	SourceName           string
	DestinationName      string
	SourceRouteName      string
	DestinationRouteName string
	KubeConfigPath       string
	ClusterId            string
	ClusterRestEndpoint  string
	ClusterLinkName      string
	Topics               []string
	AuthMode             string
	CCBootstrapEndpoint  string
	LoadBalancerEndpoint string
}

// NewMigrationConfig creates a new MigrationConfig with the given ID and options
func NewMigrationConfig(migrationId string, opts MigrationConfigOpts) *MigrationConfig {
	return &MigrationConfig{
		MigrationId:          migrationId,
		CurrentState:         StateUninitialized,
		GatewayNamespace:     opts.GatewayNamespace,
		GatewayCrdName:       opts.GatewayCrdName,
		SourceName:           opts.SourceName,
		DestinationName:      opts.DestinationName,
		SourceRouteName:      opts.SourceRouteName,
		DestinationRouteName: opts.DestinationRouteName,
		KubeConfigPath:       opts.KubeConfigPath,
		ClusterId:            opts.ClusterId,
		ClusterRestEndpoint:  opts.ClusterRestEndpoint,
		ClusterLinkName:      opts.ClusterLinkName,
		Topics:               opts.Topics,
		AuthMode:             opts.AuthMode,
		CCBootstrapEndpoint:  opts.CCBootstrapEndpoint,
		LoadBalancerEndpoint: opts.LoadBalancerEndpoint,
	}
}
