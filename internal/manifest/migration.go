// Package manifest defines the migration manifest (migration.yaml) schema
// and a strict parser. (Validation is added in a later change.)
package manifest

//go:generate go run ./schemagen/gen

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
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name string `yaml:"name" json:"name"`
}

type Spec struct {
	Source      Source       `yaml:"source" json:"source"`
	Target      Target       `yaml:"target" json:"target"`
	ClusterLink *ClusterLink `yaml:"clusterLink,omitempty" json:"clusterLink,omitempty"`
	Topics      *Topics      `yaml:"topics,omitempty" json:"topics,omitempty"`
	ACLs        *ACLs        `yaml:"acls,omitempty" json:"acls,omitempty"`
	Schemas     *Schemas     `yaml:"schemas,omitempty" json:"schemas,omitempty"`
	Connectors  *Connectors  `yaml:"connectors,omitempty" json:"connectors,omitempty"`
}

type Source struct {
	Type        string `yaml:"type" json:"type"`
	Credentials string `yaml:"credentials" json:"credentials"`
}

type Target struct {
	Type           string       `yaml:"type" json:"type"`
	Credentials    string       `yaml:"credentials" json:"credentials"`
	Cluster        string       `yaml:"cluster,omitempty" json:"cluster,omitempty"`
	Kafka          *TargetKafka `yaml:"kafka,omitempty" json:"kafka,omitempty"`
	SchemaRegistry *Endpoint    `yaml:"schemaRegistry,omitempty" json:"schemaRegistry,omitempty"`
	Connect        *Endpoint    `yaml:"connect,omitempty" json:"connect,omitempty"`
}

type TargetKafka struct {
	RestEndpoint     string   `yaml:"restEndpoint" json:"restEndpoint"`
	BootstrapServers []string `yaml:"bootstrapServers,omitempty" json:"bootstrapServers,omitempty"`
}

type Endpoint struct {
	URL string `yaml:"url" json:"url"`
}

type ClusterLink struct {
	Name    string            `yaml:"name" json:"name"`
	Configs map[string]string `yaml:"configs,omitempty" json:"configs,omitempty"`
}

type Topics struct {
	Mode    string   `yaml:"mode" json:"mode"`
	Include []string `yaml:"include" json:"include"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	Prefix  string   `yaml:"prefix,omitempty" json:"prefix,omitempty"`
}

// ACLs, Schemas, and Connectors are provisional ("stub") sections for this
// phase: their shape may change when their per-resource designs land.
type ACLs struct {
	Include          []string          `yaml:"include" json:"include"`
	Exclude          []string          `yaml:"exclude,omitempty" json:"exclude,omitempty"`
	PrincipalMapping map[string]string `yaml:"principalMapping,omitempty" json:"principalMapping,omitempty"`
}

type Schemas struct {
	Include []string `yaml:"include" json:"include"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

type Connectors struct {
	Include []string `yaml:"include" json:"include"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}
