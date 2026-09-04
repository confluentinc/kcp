// Package migplan is the I/O layer of the migration reconciliation engine: it
// gathers the four live inputs through provider interfaces and hands plain data
// to the pure core (the reconcile sub-package). The core never imports this
// package, so the package boundary enforces the two-layer split.
package migplan

import (
	"context"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// GatewayConfigSource loads the current gateway config (a static file in the
// prototype, a live k8s pull later), resolved down to the single route the
// migration targets.
type GatewayConfigSource interface {
	Load(ctx context.Context) (*reconcile.GatewayConfig, error)
}

// TopicLister lists topics on one cluster (source or target). Implementations
// list non-internal topics only.
type TopicLister interface {
	ListTopics(ctx context.Context) ([]string, error)
}

// LinkStatusProvider reports the cluster link's per-topic mirror state and the
// link-level consumer-offset-sync setting.
type LinkStatusProvider interface {
	LinkStatus(ctx context.Context) (*LinkStatus, error)
}

// LinkStatus is the cluster-link view the engine needs. Mirrors is keyed by the
// SOURCE topic name (the provider resolves any mirror-name prefix).
type LinkStatus struct {
	OffsetSyncEnabled bool
	Mirrors           map[string]reconcile.MirrorState
}
