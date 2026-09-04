package migplan

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// ReconciliationEngine is the I/O layer: it holds the four provider seams,
// performs the live reads, and hands plain data to the pure core.
type ReconciliationEngine struct {
	gateway GatewayConfigSource
	source  TopicLister
	target  TopicLister
	link    LinkStatusProvider
}

func NewReconciliationEngine(gateway GatewayConfigSource, source, target TopicLister, link LinkStatusProvider) *ReconciliationEngine {
	return &ReconciliationEngine{gateway: gateway, source: source, target: target, link: link}
}

// Run gathers the four live inputs and reconciles. A provider read failure is
// returned as an error; a feasibility refusal is carried in the *Plan's Report
// (with Artifacts nil), not as an error.
func (e *ReconciliationEngine) Run(ctx context.Context, in reconcile.ReconcileInput) (*reconcile.Plan, error) {
	gw, err := e.gateway.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading gateway config: %w", err)
	}
	src, err := e.source.ListTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing source topics: %w", err)
	}
	tgt, err := e.target.ListTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing target topics: %w", err)
	}
	link, err := e.link.LinkStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading cluster-link status: %w", err)
	}
	return reconcile.Reconcile(in, gw, src, tgt, link.Mirrors, link.OffsetSyncEnabled), nil
}
