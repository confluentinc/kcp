package targets

import (
	"context"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

// Target is the destination of a migration (feasibility §7.4). Phase 1 carries
// only the cluster-link operations; it grows per resource deep-dive.
type Target interface {
	ClusterID(ctx context.Context) (string, error)
	// GetClusterLink returns (nil, nil) when no link of that name exists.
	GetClusterLink(ctx context.Context, name string) (*clusterlink.ClusterLink, error)
	CreateClusterLink(ctx context.Context, name string, req clusterlink.CreateClusterLinkRequest) error
}
