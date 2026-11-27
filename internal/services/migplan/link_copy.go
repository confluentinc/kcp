package migplan

import (
	"context"
	"errors"
	"fmt"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// LinkCopyProvider reads one cluster's copy of a bidirectional cluster link.
type LinkCopyProvider interface {
	LinkCopy(ctx context.Context) (reconcile.LinkCopy, error)
}

var _ LinkCopyProvider = (*ClusterLinkCopy)(nil)

// ClusterLinkCopy reads one copy of a bidirectional link over that cluster's
// REST API. Each copy needs its own: a cluster lists only its own copy's mirror
// rows, and TLS trust is set per service.
type ClusterLinkCopy struct {
	svc linkReader
	cfg clusterlink.Config
}

func NewClusterLinkCopy(svc linkReader, cfg clusterlink.Config) *ClusterLinkCopy {
	return &ClusterLinkCopy{svc: svc, cfg: cfg}
}

// LinkCopy reads the copy's describe, its configs and its mirror rows. A copy
// that was never created on this cluster is Found false, not an error: a 404,
// or the nameless UNMANAGED_SOURCE entry that creating the link on the other
// cluster leaves here.
func (c *ClusterLinkCopy) LinkCopy(ctx context.Context) (reconcile.LinkCopy, error) {
	link, err := c.svc.GetClusterLink(ctx, c.cfg)
	if errors.Is(err, clusterlink.ErrLinkNotFound) {
		return reconcile.LinkCopy{}, nil
	}
	if err != nil {
		return reconcile.LinkCopy{}, fmt.Errorf("describing cluster link: %w", err)
	}
	if link == nil || link.LinkName == "" {
		return reconcile.LinkCopy{}, nil
	}

	configs, err := c.svc.ListConfigs(ctx, c.cfg)
	if err != nil {
		return reconcile.LinkCopy{}, fmt.Errorf("reading cluster-link configs: %w", err)
	}
	mirrors, err := c.svc.ListMirrorTopics(ctx, c.cfg)
	if err != nil {
		return reconcile.LinkCopy{}, fmt.Errorf("listing mirror topics: %w", err)
	}
	rows := make(map[string]reconcile.MirrorRow, len(mirrors))
	for _, m := range mirrors {
		rows[m.MirrorTopicName] = reconcile.MirrorRow{Status: mapRowStatus(m.MirrorStatus), Raw: m.MirrorStatus}
	}
	return reconcile.LinkCopy{
		Found:             true,
		LinkID:            link.LinkID,
		RemoteClusterID:   link.RemoteClusterID,
		LinkError:         link.LinkError,
		Mode:              configs[linkModeConfig],
		Prefix:            configs[manifest.LinkConfigPrefix],
		OffsetSyncEnabled: configs[manifest.LinkConfigOffsetSyncEnable] == "true",
		GroupFilters:      configs[manifest.LinkConfigOffsetGroupFilters],
		Rows:              rows,
	}, nil
}

// mapRowStatus keeps the transitional statuses of a reversal apart; any status
// it doesn't name is RowOther, which the core refuses.
func mapRowStatus(status string) reconcile.RowStatus {
	switch status {
	case clusterlink.MirrorStatusActive:
		return reconcile.RowActive
	case clusterlink.MirrorStatusStopped:
		return reconcile.RowStopped
	case clusterlink.MirrorStatusPendingSynchronize:
		return reconcile.RowPendingSynchronize
	case clusterlink.MirrorStatusPendingMirror:
		return reconcile.RowPendingMirror
	case clusterlink.MirrorStatusPendingStopped:
		return reconcile.RowPendingStopped
	}
	return reconcile.RowOther
}
