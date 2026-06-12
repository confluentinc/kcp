package sources

import (
	"context"

	"github.com/confluentinc/kcp/internal/types"
)

// Source represents a Kafka source (MSK or OSK) that can be scanned
type Source interface {
	// Type returns the source type (msk or osk)
	Type() types.SourceType

	// LoadCredentials loads authentication credentials from a file
	LoadCredentials(credentialsPath string) error

	// Scan performs discovery/scanning of the source clusters
	Scan(ctx context.Context, opts ScanOptions) (*ScanResult, error)

	// GetClusters returns the list of clusters available to scan
	GetClusters() []ClusterIdentifier
}

// ClusterIdentifier uniquely identifies a cluster within a source
type ClusterIdentifier struct {
	Name             string   // Human-readable name (MSK: cluster name, OSK: user ID)
	UniqueID         string   // Unique identifier (MSK: ARN, OSK: user ID)
	BootstrapServers []string // Bootstrap server addresses
}

// ScanOptions contains options for scanning
type ScanOptions struct {
	SkipTopics bool
	SkipACLs   bool
	// State is the existing kcp state. Required for MSK scanning (broker addresses
	// come from prior kcp discover output). Ignored by OSK.
	State *types.State
}

// ScanResult contains the results of scanning a source
type ScanResult struct {
	SourceType types.SourceType
	Clusters   []ClusterScanResult
}

// ClusterScanResult contains scan results for a single cluster
type ClusterScanResult struct {
	Identifier         ClusterIdentifier
	KafkaAdminInfo     *types.KafkaAdminClientInformation
	SourceSpecificData interface{} // MSK: AWSClientInformation, OSK: OSKClusterMetadata
}
