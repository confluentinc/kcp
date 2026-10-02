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

	// sourceGroups/targetGroups are set by WithGroupListers; only a route
	// conversion reads them.
	sourceGroups GroupLister
	targetGroups GroupLister
}

func NewReconciliationEngine(gateway GatewayConfigSource, source, target TopicLister, link LinkStatusProvider, secrets SecretExistenceChecker) *ReconciliationEngine {
	return &ReconciliationEngine{gateway: gateway, source: source, target: target, link: link, secrets: secrets}
}

// WithGroupListers adds the consumer-group listers a route conversion needs for
// its split-brain check. A topic migration never calls them.
func (e *ReconciliationEngine) WithGroupListers(source, target GroupLister) *ReconciliationEngine {
	e.sourceGroups, e.targetGroups = source, target
	return e
}

// Run gathers the live inputs and reconciles. A provider read failure is
// returned as an error; a feasibility refusal is carried in the *Plan's Report
// (with Artifacts nil), not as an error. The secrets provider is only
// consulted for a static-mode route — a dynamic-route migration has no
// redundant-auth concept, so no live secret lookup is made for one.
// A route conversion (in.ConvertTo set) also lists consumer groups on both
// clusters, fetches the topics the source groups have committed on, and
// reconciles through reconcile.ReconcileConvert; the secrets provider is not
// consulted for it.
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

	if in.ConvertTo != "" {
		return e.runConvert(ctx, in, gw, src, tgt, link, ids)
	}

	var missingSecrets []string
	var secretCheckSkipped string
	if gw != nil && gw.Route != nil && gw.Route.Mode == "static" {
		if names := reconcile.ResolveStagedSecretNames(gw, in.TargetDomain); len(names) > 0 {
			missingSecrets, secretCheckSkipped, err = e.secrets.MissingSecrets(ctx, names)
			if err != nil {
				return nil, fmt.Errorf("checking staged auth secrets: %w", err)
			}
		}
	}

	plan := reconcile.Reconcile(in, gw, src, tgt, link.Mirrors, link.OffsetSyncEnabled, ids, missingSecrets, secretCheckSkipped)
	// A permission denial is a skip, not a precondition failure — surfaced as
	// a warning (never blocking) rather than folded into missingSecrets,
	// which would otherwise read as "these specific secrets don't exist"
	// when the truth is "we couldn't check at all". See
	// SecretExistenceChecker's own doc comment for why this distinction
	// matters. Reconcile also threads secretCheckSkipped into the "staged
	// auth secrets exist" precondition itself (Skipped: true), so the
	// rendered report never shows a green ✓ for a check that never ran —
	// this warning and that precondition are two views of the same fact,
	// not a contradiction.
	if secretCheckSkipped != "" {
		plan.Report.Warnings = append(plan.Report.Warnings, secretCheckSkipped)
	}
	// Carry the gateway CR the plan was computed against, so a caller can re-pull
	// it before mutating and diff for drift.
	plan.GatewayYAML = gw.RawYAML
	return plan, nil
}

// runConvert gathers the consumer-group data a conversion needs (both clusters'
// listings, and the topics the source groups have committed on) and reconciles
// it. A listing or fetch failure is an I/O error, never a refusal: a partial
// result could hide a split-brain or leave a tracked topic unverified.
func (e *ReconciliationEngine) runConvert(ctx context.Context, in reconcile.ReconcileInput, gw *reconcile.GatewayConfig,
	src, tgt []string, link *LinkStatus, ids reconcile.ClusterIDs) (*reconcile.Plan, error) {
	if e.sourceGroups == nil || e.targetGroups == nil {
		return nil, fmt.Errorf("a route conversion needs consumer-group listers for both clusters")
	}
	// Checked before each listing is trusted: a credential that may not describe
	// arbitrary groups gets a silently filtered list, not an error.
	srcCan, err := e.sourceGroups.CanDescribeAnyGroup(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking the source credential's group access: %w", err)
	}
	tgtCan, err := e.targetGroups.CanDescribeAnyGroup(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking the destination credential's group access: %w", err)
	}
	srcTopics, err := e.sourceGroups.CanDescribeAnyTopic(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking the source credential's topic access: %w", err)
	}
	tgtTopics, err := e.targetGroups.CanDescribeAnyTopic(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking the destination credential's topic access: %w", err)
	}
	srcGroups, err := e.sourceGroups.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing source consumer groups: %w", err)
	}
	tgtGroups, err := e.targetGroups.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing destination consumer groups: %w", err)
	}
	srcIDs := make([]string, 0, len(srcGroups))
	for _, g := range srcGroups {
		srcIDs = append(srcIDs, g.GroupID)
	}
	tracked, err := e.sourceGroups.CommittedTopics(ctx, srcIDs)
	if err != nil {
		return nil, fmt.Errorf("fetching committed offsets of source consumer groups: %w", err)
	}
	plan := reconcile.ReconcileConvert(in, gw, src, tgt, link.Mirrors, link.OffsetSyncEnabled, ids, groupFacts(srcGroups, tgtGroups, tracked, listingGaps{
		sourceGroups: listingGap("source", srcCan),
		targetGroups: listingGap("destination", tgtCan),
		sourceTopics: topicGap("source", srcTopics),
		targetTopics: topicGap("destination", tgtTopics),
	}))
	plan.GatewayYAML = gw.RawYAML
	return plan, nil
}
