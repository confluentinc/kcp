package types

import "time"

// OSKSourcesState contains all OSK-specific data
type OSKSourcesState struct {
	Clusters []OSKDiscoveredCluster `json:"clusters"`
}

// OSKDiscoveredCluster represents a discovered OSK cluster
type OSKDiscoveredCluster struct {
	ID                          string                      `json:"id"`
	BootstrapServers            []string                    `json:"bootstrap_servers"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	ClusterMetrics              *ProcessedClusterMetrics    `json:"metrics,omitempty"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
	Metadata                    OSKClusterMetadata          `json:"metadata"`
}

// OSKClusterMetadata contains optional metadata about OSK clusters
type OSKClusterMetadata struct {
	Environment  string            `json:"environment,omitempty"`
	Location     string            `json:"location,omitempty"`
	KafkaVersion string            `json:"kafka_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	LastScanned  time.Time         `json:"last_scanned"`
}
