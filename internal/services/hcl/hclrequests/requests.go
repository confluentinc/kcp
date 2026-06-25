package hclrequests

import "github.com/confluentinc/kcp/internal/types"

type TargetClusterWizardRequest struct {
	AwsRegion              string   `json:"aws_region"`
	NeedsEnvironment       bool     `json:"needs_environment"`
	EnvironmentName        string   `json:"environment_name"`
	EnvironmentId          string   `json:"environment_id"`
	NeedsCluster           bool     `json:"needs_cluster"`
	ClusterName            string   `json:"cluster_name"`
	ClusterType            string   `json:"cluster_type"`
	ClusterAvailability    string   `json:"cluster_availability"` // "SINGLE_ZONE" or "MULTI_ZONE"
	ClusterCku             int      `json:"cluster_cku"`          // Number of CKUs (1+, MULTI_ZONE requires >= 2)
	NeedsPrivateLink       bool     `json:"needs_private_link"`
	UseExistingRoute53Zone bool     `json:"use_existing_route53_zone"`
	PreventDestroy         bool     `json:"prevent_destroy"`
	VpcId                  string   `json:"vpc_id"`
	SubnetCidrRanges       []string `json:"subnet_cidr_ranges"`
}

type MigrationWizardRequest struct {
	HasPublicEndpoints bool `json:"has_public_brokers"`

	VpcId string `json:"vpc_id"`

	UseJumpClusters            bool                            `json:"use_jump_clusters"`
	ExtOutboundSecurityGroupId string                          `json:"ext_outbound_security_group_id"`
	ExtOutboundSubnetId        string                          `json:"ext_outbound_subnet_id"`
	ExtOutboundBrokers         []ExtOutboundClusterKafkaBroker `json:"source_kafka_brokers"`

	ExistingPrivateLinkVpceId string `json:"existing_private_link_vpce_id"`

	HasExistingInternetGateway bool `json:"has_existing_internet_gateway"`

	JumpClusterInstanceType        string   `json:"jump_cluster_instance_type"`
	JumpClusterBrokerStorage       int      `json:"jump_cluster_broker_storage"`
	JumpClusterBrokerSubnetCidr    []string `json:"jump_cluster_broker_subnet_cidr"`
	JumpClusterSetupHostSubnetCidr string   `json:"jump_cluster_setup_host_subnet_cidr"`

	JumpClusterAuthType             string `json:"jump_cluster_auth_type"`
	SourceClusterId                 string `json:"source_cluster_id"`
	JumpClusterIamAuthRoleName      string `json:"jump_cluster_iam_auth_role_name"`
	SourceSaslScramBootstrapServers string `json:"source_sasl_scram_bootstrap_servers"`
	SourceSaslScramMechanism        string `json:"source_sasl_scram_mechanism"`
	SourcePlaintextBootstrapServers string `json:"source_plaintext_bootstrap_servers"`
	SourceSaslIamBootstrapServers   string `json:"source_sasl_iam_bootstrap_servers"`
	SourceRegion                    string `json:"source_region"`
	TargetEnvironmentId             string `json:"target_environment_id"`
	TargetClusterId                 string `json:"target_cluster_id"`
	TargetRestEndpoint              string `json:"target_rest_endpoint"`
	TargetBootstrapEndpoint         string `json:"target_bootstrap_endpoint"`
	ClusterLinkName                 string `json:"cluster_link_name"`
	TargetClusterType               string `json:"target_cluster_type"`
}

type ExtOutboundClusterKafkaBroker struct {
	ID        string                            `json:"broker_id"`
	SubnetID  string                            `json:"subnet_id"`
	Endpoints []ExtOutboundClusterKafkaEndpoint `json:"endpoints"`
}

type ExtOutboundClusterKafkaEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	IP   string `json:"ip"`
}

type MigrateAclsRequest struct {
	SelectedPrincipals        []string `json:"selected_principals"`
	TargetClusterId           string   `json:"target_cluster_id"`
	TargetClusterRestEndpoint string   `json:"target_cluster_rest_endpoint"`
	PreventDestroy            bool     `json:"prevent_destroy"`

	SourceType string `json:"source_type"`
	ClusterId  string `json:"cluster_id"`

	// This is not sent by the UI payload but instead built by the API service before being passed on to the HCL service.
	AclsByPrincipal map[string][]types.Acls `json:"-"`
}

// MigrateTopicsMode values for MirrorTopicsRequest.Mode.
const (
	MigrateTopicsModeMirror = "mirror"
	MigrateTopicsModeNew    = "new"
)

// MirrorTopicsRequest carries the inputs for `kcp create-asset migrate-topics` in
// both --mode mirror and --mode new. The name is kept for backward compatibility
// with the UI wire format; consider renaming to MigrateTopicsRequest in a future change.
type MirrorTopicsRequest struct {
	// SelectedTopics is the legacy name-only list sent by the UI wizard. The CLI
	// populates Topics instead; the HCL service falls back to SelectedTopics when
	// Topics is empty (mirror-mode UI path only — new mode requires Topics).
	SelectedTopics []string `json:"selected_topics"`
	// Topics carries the full topic details (partitions, configs) needed by new mode.
	// Not part of the JSON wire format — the CLI populates this from state directly.
	Topics                    []types.TopicDetails `json:"-"`
	ClusterLinkName           string               `json:"cluster_link_name"`
	TargetClusterId           string               `json:"target_cluster_id"`
	TargetClusterRestEndpoint string               `json:"target_cluster_rest_endpoint"`
	// Mode selects the generator: "mirror" emits confluent_kafka_mirror_topic
	// resources; "new" emits confluent_kafka_topic resources with no data forward.
	Mode            string   `json:"mode"`
	IncludePatterns []string `json:"topics_include"`
	ExcludePatterns []string `json:"topics_exclude"`

	// SourceType and ClusterId identify the source cluster in the loaded state
	// file. The UI wizard sends these as hidden fields so the API handler can
	// hydrate Topics from state for --mode new. The CLI populates Topics
	// directly and leaves these empty.
	SourceType string `json:"source_type"`
	ClusterId  string `json:"cluster_id"`
}

type ReverseProxyRequest struct {
	Region                                 string `json:"region"`
	VPCId                                  string `json:"vpc_id"`
	PublicSubnetCidr                       string `json:"public_subnet_cidr"`
	ConfluentCloudClusterBootstrapEndpoint string `json:"confluent_cloud_cluster_bootstrap_endpoint"`
}

type BastionHostRequest struct {
	Region                     string   `json:"region"`
	VPCId                      string   `json:"vpc_id"`
	PublicSubnetCidr           string   `json:"public_subnet_cidr"`
	HasExistingInternetGateway bool     `json:"has_existing_internet_gateway"`
	SecurityGroupIds           []string `json:"security_group_ids"`
}

type MigrateSchemasRequest struct {
	ConfluentCloudSchemaRegistryURL string                         `json:"confluent_cloud_schema_registry_url"`
	SchemaRegistries                []SchemaRegistryExporterConfig `json:"schema_registries"`
}

type SchemaRegistryExporterConfig struct {
	Migrate   bool     `json:"migrate"`
	Subjects  []string `json:"subjects"`
	SourceURL string   `json:"source_url"`
}

type MigrateGlueSchemasRequest struct {
	ConfluentCloudSchemaRegistryURL string                              `json:"confluent_cloud_schema_registry_url"`
	GlueRegistries                  []GlueSchemaRegistryMigrationConfig `json:"glue_registries"`
}

type GlueSchemaRegistryMigrationConfig struct {
	Migrate      bool               `json:"migrate"`
	RegistryName string             `json:"registry_name"`
	Region       string             `json:"region"`
	Schemas      []types.GlueSchema `json:"schemas"`
}
