package providers

import (
	"context"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// mirrorLister is the narrow slice of clusterlink.Service this provider needs.
type mirrorLister interface {
	ListMirrorTopics(ctx context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error)
}

var _ migplan.LinkStatusProvider = (*ClusterLinkStatus)(nil)

// ClusterLinkStatus reports the link's per-topic mirror state (keyed by SOURCE
// topic name) and its offset-sync setting. It implements migplan.LinkStatusProvider.
//
// offsetSyncEnabled is supplied by the caller: the clusterlink service on this
// branch exposes no read for the link's consumer.offset.sync.enable config, so
// the value is passed in rather than read live. Reading it live (via a link
// config describe) is a later addition.
type ClusterLinkStatus struct {
	svc               mirrorLister
	cfg               clusterlink.Config
	offsetSyncEnabled bool
}

func NewClusterLinkStatus(svc mirrorLister, cfg clusterlink.Config, offsetSyncEnabled bool) *ClusterLinkStatus {
	return &ClusterLinkStatus{svc: svc, cfg: cfg, offsetSyncEnabled: offsetSyncEnabled}
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
	return &migplan.LinkStatus{OffsetSyncEnabled: c.offsetSyncEnabled, Mirrors: m}, nil
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
