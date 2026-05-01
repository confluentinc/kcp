package utils

import (
	"github.com/confluentinc/kcp/internal/types"
)

// GetClusterDisplayName returns a human-readable cluster identifier for logging.
// For MSK sources, it extracts the cluster name from the ARN.
// For OSK sources, it returns the cluster ID.
func GetClusterDisplayName(sourceType types.SourceType, clusterArn string, clusterID string) string {
	switch sourceType {
	case types.SourceTypeMSK:
		if clusterArn == "" {
			return "unknown-cluster"
		}
		return ExtractClusterNameFromArn(clusterArn)
	case types.SourceTypeOSK:
		if clusterID == "" {
			return "unknown-cluster"
		}
		return clusterID
	default:
		return "unknown-cluster"
	}
}
