package types

// MigrationConfig holds all domain configuration for a migration
// This is pure data with no behavior - just fields that get serialized
type MigrationConfig struct {
	MigrationId  string `json:"migration_id"`
	CurrentState string `json:"current_state"`

	// Gateway configuration
	KubeConfigPath string `json:"kube_config_path"`

	// Source cluster configuration
	SourceClusterArn string `json:"source_cluster_arn"`

	// Cluster link configuration
	ClusterId           string   `json:"cluster_id"`
	ClusterRestEndpoint string   `json:"cluster_rest_endpoint"`
	ClusterLinkName     string   `json:"cluster_link_name"`
	Topics              []string `json:"topics"`
	AuthMode            string   `json:"auth_mode"`

	// Migration runtime data (populated during initialization)
	ClusterLinkTopics  []string          `json:"cluster_link_topics"`
	ClusterLinkConfigs map[string]string `json:"cluster_link_configs"`

	// Gateway CR configuration
	PassthroughCrName string `json:"passthrough_cr_name"`
	K8sNamespace      string `json:"k8s_namespace"`
	InitialCrYAML     []byte `json:"initial_cr_yaml"`
	FencedCrYAML      []byte `json:"fenced_cr_yaml"`
	SwitchoverCrYAML  []byte `json:"switchover_cr_yaml"`
}

