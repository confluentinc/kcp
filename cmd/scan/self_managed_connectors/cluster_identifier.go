package self_managed_connectors

import (
	"github.com/confluentinc/kcp/internal/utils"
)

// GetClusterDisplayName returns a human-readable cluster identifier for logging.
// For MSK sources, it extracts the cluster name from the ARN.
// For OSK sources, it returns the cluster ID.
func GetClusterDisplayName(sourceType string, clusterArn string, clusterID string) string {
	switch sourceType {
	case "msk":
		if clusterArn == "" {
			return "unknown-cluster"
		}
		return utils.ExtractClusterNameFromArn(clusterArn)
	case "osk":
		if clusterID == "" {
			return "unknown-cluster"
		}
		return clusterID
	default:
		return "unknown-cluster"
	}
}
