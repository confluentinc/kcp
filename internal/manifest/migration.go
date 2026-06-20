// Package manifest defines the migration manifest (migration.yaml) schema
// and a strict parser. (Validation is added in a later change.)
package manifest

const (
	SupportedAPIVersion = "kcp.confluent.io/v1alpha1"
	KindMigration       = "Migration"

	// SourceApacheKafka is the only source type supported in this phase.
	// MSK support is deferred to a later phase.
	SourceApacheKafka = "apache-kafka"

	TargetConfluentCloud    = "confluent-cloud"
	TargetConfluentPlatform = "confluent-platform"

	TopicModeMirror = "mirror"
	TopicModeNew    = "new"
)

// Migration is the top-level migration manifest.
type Migration struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

type Spec struct {
	Source      Source       `yaml:"source"`
	Target      Target       `yaml:"target"`
	ClusterLink *ClusterLink `yaml:"clusterLink,omitempty"`
	Topics      *Topics      `yaml:"topics,omitempty"`
	ACLs        *ACLs        `yaml:"acls,omitempty"`
	Schemas     *Schemas     `yaml:"schemas,omitempty"`
	Connectors  *Connectors  `yaml:"connectors,omitempty"`
}

type Source struct {
	Type        string `yaml:"type"`
	Credentials string `yaml:"credentials"`
}

type Target struct {
	Type           string       `yaml:"type"`
	Credentials    string       `yaml:"credentials"`
	Cluster        string       `yaml:"cluster,omitempty"`
	Kafka          *TargetKafka `yaml:"kafka,omitempty"`
	SchemaRegistry *Endpoint    `yaml:"schemaRegistry,omitempty"`
	Connect        *Endpoint    `yaml:"connect,omitempty"`
}

type TargetKafka struct {
	RestEndpoint     string   `yaml:"restEndpoint"`
	BootstrapServers []string `yaml:"bootstrapServers,omitempty"`
}

type Endpoint struct {
	URL string `yaml:"url"`
}

type ClusterLink struct {
	Name    string            `yaml:"name"`
	Configs map[string]string `yaml:"configs,omitempty"`
}

type Topics struct {
	Mode    string   `yaml:"mode"`
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude,omitempty"`
	Prefix  string   `yaml:"prefix,omitempty"`
}

// ACLs, Schemas, and Connectors are provisional ("stub") sections for this
// phase: their shape may change when their per-resource designs land.
type ACLs struct {
	Include          []string          `yaml:"include"`
	Exclude          []string          `yaml:"exclude,omitempty"`
	PrincipalMapping map[string]string `yaml:"principalMapping,omitempty"`
}

type Schemas struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude,omitempty"`
}

type Connectors struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude,omitempty"`
}
