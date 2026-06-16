package types

import "time"

// ApacheKafkaSourcesState contains all Apache Kafka-specific data
type ApacheKafkaSourcesState struct {
	Clusters []ApacheKafkaDiscoveredCluster `json:"clusters"`
}

// ApacheKafkaDiscoveredCluster represents a discovered Apache Kafka cluster
type ApacheKafkaDiscoveredCluster struct {
	ID                          string                      `json:"id"`
	BootstrapServers            []string                    `json:"bootstrap_servers"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	ClusterMetrics              *ProcessedClusterMetrics    `json:"metrics,omitempty"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
	Metadata                    ApacheKafkaClusterMetadata  `json:"metadata"`
}

// ApacheKafkaClusterMetadata contains optional metadata about Apache Kafka clusters
type ApacheKafkaClusterMetadata struct {
	Environment  string            `json:"environment,omitempty"`
	Location     string            `json:"location,omitempty"`
	KafkaVersion string            `json:"kafka_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	LastScanned  time.Time         `json:"last_scanned"`
}
