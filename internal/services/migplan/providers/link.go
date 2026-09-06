package providers

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// offsetSyncEnableConfig is the cluster-link config key that governs consumer
// offset sync; a dynamic-route migration requires it disabled.
const offsetSyncEnableConfig = "consumer.offset.sync.enable"

// linkReader is the narrow slice of clusterlink.Service this provider needs: the
// link's mirror topics, its live config (consumer.offset.sync.enable), and its
// describe (for source_cluster_id).
type linkReader interface {
	ListMirrorTopics(ctx context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error)
	ListConfigs(ctx context.Context, config clusterlink.Config) (map[string]string, error)
	GetClusterLink(ctx context.Context, config clusterlink.Config) (*clusterlink.ClusterLink, error)
}

var _ migplan.LinkStatusProvider = (*ClusterLinkStatus)(nil)

// ClusterLinkStatus reports the link's per-topic mirror state (keyed by SOURCE
// topic name) and whether consumer offset sync is enabled — both read live from
// the destination cluster link. It implements migplan.LinkStatusProvider.
type ClusterLinkStatus struct {
	svc linkReader
	cfg clusterlink.Config
}

func NewClusterLinkStatus(svc linkReader, cfg clusterlink.Config) *ClusterLinkStatus {
	return &ClusterLinkStatus{svc: svc, cfg: cfg}
}

func (c *ClusterLinkStatus) LinkStatus(ctx context.Context) (*migplan.LinkStatus, error) {
	mirrors, err := c.svc.ListMirrorTopics(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	m := make(map[string]reconcile.MirrorState, len(mirrors))
	for _, mt := range mirrors {
		m[mt.SourceTopicName] = mapStatus(mt.MirrorStatus)
	}

	configs, err := c.svc.ListConfigs(ctx, c.cfg)
	if err != nil {
		return nil, fmt.Errorf("reading cluster-link configs: %w", err)
	}
	// Absent ⇒ the link default, which is disabled; only an explicit "true" is on.
	enabled := configs[offsetSyncEnableConfig] == "true"

	link, err := c.svc.GetClusterLink(ctx, c.cfg)
	if err != nil {
		return nil, fmt.Errorf("describing cluster link: %w", err)
	}

	return &migplan.LinkStatus{
		OffsetSyncEnabled: enabled,
		Mirrors:           m,
		SourceClusterID:   link.SourceClusterID,
	}, nil
}

// mapStatus maps a cluster-link mirror status to the core's MirrorState.
// ACTIVE → mirroring, STOPPED → promoted, anything else → bad (fail-fast).
func mapStatus(status string) reconcile.MirrorState {
	switch status {
	case clusterlink.MirrorStatusActive:
		return reconcile.MirrorActive
	case clusterlink.MirrorStatusStopped:
		return reconcile.MirrorStopped
	default:
		return reconcile.MirrorBad
	}
}
