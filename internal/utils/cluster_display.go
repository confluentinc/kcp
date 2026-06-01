package utils

import (
	"fmt"

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

// InferSourceTypeFromClusterID resolves a cluster identifier to its source type
// by looking it up in the state file. MSK clusters are matched by ARN; OSK
// clusters are matched by ID. Returns an error if the cluster is registered
// under neither.
func InferSourceTypeFromClusterID(state *types.State, clusterID string) (types.SourceType, error) {
	if state != nil {
		if _, err := state.GetClusterByArn(clusterID); err == nil {
			return types.SourceTypeMSK, nil
		}
		if _, err := state.GetOSKClusterByID(clusterID); err == nil {
			return types.SourceTypeOSK, nil
		}
	}
	return "", fmt.Errorf("cluster %q not found in state file as either an MSK cluster (by ARN) or an OSK cluster (by ID)", clusterID)
}
