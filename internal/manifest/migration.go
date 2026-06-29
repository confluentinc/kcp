// Package manifest defines the migration manifest (migration.yaml) schema
// and a strict parser. (Validation is added in a later change.)
package manifest

//go:generate go run ./schemagen/gen

const (
	SupportedAPIVersion = "kcp.confluent.io/v1alpha1"
	KindMigration       = "Migration"

	// SourceApacheKafka covers any non-Confluent Kafka source (Apache Kafka, MSK,
	// OSK). Such sources can only be the source of a destination-initiated link —
	// they cannot initiate (host a link object).
	SourceApacheKafka = "apache-kafka"
	// SourceMSK is an AWS MSK source. Distinct from apache-kafka so validation can
	// gate IAM (only MSK uses it). Like apache-kafka, it cannot initiate a link.
	SourceMSK = "msk"
	// SourceConfluentPlatform is a Confluent Platform source. Required for
	// source-initiated ("external") links, where the source dials out to the
	// destination (e.g. a private on-prem CP migrating to public Confluent Cloud).
	SourceConfluentPlatform = "confluent-platform"

	TargetConfluentCloud    = "confluent-cloud"
	TargetConfluentPlatform = "confluent-platform"

	TopicModeMirror = "mirror"
	TopicModeNew    = "new"

	// ClusterLinkModeDestination is the default cluster-link mode: the link is
	// created on the destination (target) cluster and pulls from the source.
	ClusterLinkModeDestination = "destination"
	// ClusterLinkModeSource creates the link on the source cluster (source-initiated),
	// pushing to the destination. Only valid when the source can initiate (CP/CC).
	ClusterLinkModeSource = "source"

	PatternTypeLiteral  = "LITERAL"
	PatternTypePrefixed = "PREFIXED"
	FilterTypeInclude   = "INCLUDE"
	FilterTypeExclude   = "EXCLUDE"
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
	Type             string   `yaml:"type" json:"type"`
	BootstrapServers []string `yaml:"bootstrapServers" json:"bootstrapServers"`
	Credentials      string   `yaml:"credentials" json:"credentials"`
}

type Target struct {
	Type           string       `yaml:"type" json:"type"`
	Credentials    string       `yaml:"credentials" json:"credentials"`
	ClusterID      string       `yaml:"clusterId,omitempty" json:"clusterId,omitempty"`
	Kafka          *TargetKafka `yaml:"kafka,omitempty" json:"kafka,omitempty"`
	SchemaRegistry *Endpoint    `yaml:"schemaRegistry,omitempty" json:"schemaRegistry,omitempty"`
	Connect        *Endpoint    `yaml:"connect,omitempty" json:"connect,omitempty"`
}

type TargetKafka struct {
	RestEndpoint string `yaml:"restEndpoint" json:"restEndpoint"`
}

type Endpoint struct {
	URL string `yaml:"url" json:"url"`
}

// KafkaConn is a Kafka connection: a bootstrap address plus the auth-only
// credentials file used to reach it. Parallels RestRef for REST endpoints.
type KafkaConn struct {
	BootstrapServers []string `yaml:"bootstrapServers" json:"bootstrapServers"`
	Credentials      string   `yaml:"credentials" json:"credentials"`
}

type ClusterLink struct {
	Name string `yaml:"name" json:"name"`
	// Mode is the cluster-link mode: "destination" (default) or "source".
	// Empty is treated as "destination", so it is optional (omitempty keeps it
	// out of the generated schema's required set, matching the validator).
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// Source is the link→source connection in destination mode (D2).
	Source *KafkaConn `yaml:"source,omitempty" json:"source,omitempty"`
	// SourceRest is the KCP→source REST endpoint for source-initiated mode.
	SourceRest *RestRef `yaml:"sourceRest,omitempty" json:"sourceRest,omitempty"`
	// Destination is the source-side link→destination connection in source mode (D5).
	Destination *KafkaConn `yaml:"destination,omitempty" json:"destination,omitempty"`
	// Prefix maps to the link config cluster.link.prefix. When set, mirror topic
	// names become prefix+sourceName (immutable once the link exists).
	Prefix             string              `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	ConsumerOffsetSync *ConsumerOffsetSync `yaml:"consumerOffsetSync,omitempty" json:"consumerOffsetSync,omitempty"`
	TopicConfigSync    *TopicConfigSync    `yaml:"topicConfigSync,omitempty" json:"topicConfigSync,omitempty"`
	Configs            map[string]string   `yaml:"configs,omitempty" json:"configs,omitempty"`
}

// RestRef references a REST endpoint plus a credentials file for it.
type RestRef struct {
	Endpoint    string `yaml:"endpoint" json:"endpoint"`
	Credentials string `yaml:"credentials" json:"credentials"`
}

// ConsumerOffsetSync configures consumer-offset migration on the link. Enable
// defaults to true when nil (including when the whole block is omitted).
type ConsumerOffsetSync struct {
	Enable       *bool         `yaml:"enable,omitempty" json:"enable,omitempty"`
	IntervalMs   int           `yaml:"intervalMs,omitempty" json:"intervalMs,omitempty"`
	GroupFilters []GroupFilter `yaml:"groupFilters,omitempty" json:"groupFilters,omitempty"`
}

// GroupFilter is one consumer.offset.group.filters entry.
type GroupFilter struct {
	Name        string `yaml:"name" json:"name"`
	PatternType string `yaml:"patternType" json:"patternType"`
	FilterType  string `yaml:"filterType" json:"filterType"`
}

// TopicConfigSync configures topic.config.sync.ms on the link.
type TopicConfigSync struct {
	IntervalMs int `yaml:"intervalMs,omitempty" json:"intervalMs,omitempty"`
}

type Topics struct {
	Mode    string   `yaml:"mode" json:"mode"`
	Include []string `yaml:"include" json:"include"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
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
