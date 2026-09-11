package migplan

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

// ReconciliationEngine is the I/O layer: it holds the five provider seams,
// performs the live reads, and hands plain data to the pure core.
type ReconciliationEngine struct {
	gateway GatewayConfigSource
	source  TopicLister
	target  TopicLister
	link    LinkStatusProvider
	secrets SecretExistenceChecker
}

func NewReconciliationEngine(gateway GatewayConfigSource, source, target TopicLister, link LinkStatusProvider, secrets SecretExistenceChecker) *ReconciliationEngine {
	return &ReconciliationEngine{gateway: gateway, source: source, target: target, link: link, secrets: secrets}
}

// Run gathers the live inputs and reconciles. A provider read failure is
// returned as an error; a feasibility refusal is carried in the *Plan's Report
// (with Artifacts nil), not as an error. The secrets provider is only
// consulted for a static-mode route — a dynamic-route migration has no
// redundant-auth concept, so no live secret lookup is made for one.
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

	// Cluster identities, to verify the clusters we read are the real source/dest.
	srcID, err := e.source.ClusterID(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading source cluster id: %w", err)
	}
	tgtID, err := e.target.ClusterID(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading target cluster id: %w", err)
	}
	ids := reconcile.ClusterIDs{Source: srcID, Target: tgtID, LinkSource: link.SourceClusterID}

	var missingSecrets []string
	if gw != nil && gw.Route != nil && gw.Route.Mode == "static" {
		if names := reconcile.ResolveStagedSecretNames(gw, in.TargetDomain); len(names) > 0 {
			missingSecrets, err = e.secrets.MissingSecrets(ctx, names)
			if err != nil {
				return nil, fmt.Errorf("checking staged auth secrets: %w", err)
			}
		}
	}

	plan := reconcile.Reconcile(in, gw, src, tgt, link.Mirrors, link.OffsetSyncEnabled, ids, missingSecrets)
	// Carry the gateway CR the plan was computed against, so a caller can re-pull
	// it before mutating and diff for drift.
	plan.GatewayYAML = gw.RawYAML
	return plan, nil
}
