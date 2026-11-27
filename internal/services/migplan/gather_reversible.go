package migplan

import (
	"context"
	"fmt"
	"sort"

	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/tracing"
)

// ReversibleProviders are the live reads of a reversible static migration: the
// gateway CR, both clusters over the Kafka protocol, both copies of the link
// over REST, and each cluster's consumer groups. Secrets nil means the staged
// auth secrets are not checked, which the plan reports as a skip.
type ReversibleProviders struct {
	Gateway                    GatewayConfigSource
	Source, Target             TopicLister
	SourceLink, TargetLink     LinkCopyProvider
	Secrets                    SecretExistenceChecker
	SourceGroups, TargetGroups GroupLister
}

func (p ReversibleProviders) groups(s reconcile.Side) GroupLister {
	if s == reconcile.SideSource {
		return p.SourceGroups
	}
	return p.TargetGroups
}

// GatherReversibleFacts reads everything ReconcileReversibleStatic decides on,
// in an order that knows each cluster's role before the group reads: the
// gateway CR, both clusters' topics and IDs, both link copies, the staged
// secrets of both domains, then, only when a reversal or switch can follow,
// the groups. Any read error is returned; a refusal is the core's to make.
func GatherReversibleFacts(ctx context.Context, in reconcile.ReversibleInput, p ReversibleProviders) (reconcile.ReversibleFacts, error) {
	var f reconcile.ReversibleFacts
	var err error
	if f.Gateway, err = p.Gateway.Load(ctx); err != nil {
		return f, fmt.Errorf("loading gateway config: %w", err)
	}
	if f.SourceTopics, err = p.Source.ListTopics(ctx); err != nil {
		return f, fmt.Errorf("listing source topics: %w", err)
	}
	if f.TargetTopics, err = p.Target.ListTopics(ctx); err != nil {
		return f, fmt.Errorf("listing target topics: %w", err)
	}
	if f.LiveSourceClusterID, err = p.Source.ClusterID(ctx); err != nil {
		return f, fmt.Errorf("reading source cluster id: %w", err)
	}
	if f.LiveTargetClusterID, err = p.Target.ClusterID(ctx); err != nil {
		return f, fmt.Errorf("reading target cluster id: %w", err)
	}
	if f.SourceLink, err = p.SourceLink.LinkCopy(ctx); err != nil {
		return f, fmt.Errorf("reading the source copy of the cluster link: %w", err)
	}
	if f.TargetLink, err = p.TargetLink.LinkCopy(ctx); err != nil {
		return f, fmt.Errorf("reading the target copy of the cluster link: %w", err)
	}

	if p.Secrets == nil {
		f.SecretCheckSkipped = "kcp did not check that the staged auth secrets exist (no Kubernetes secret access was configured)"
	} else if names := stagedSecretsOfBothDomains(f.Gateway, in); len(names) > 0 {
		if f.MissingSecrets, f.SecretCheckSkipped, err = p.Secrets.MissingSecrets(ctx, names); err != nil {
			return f, fmt.Errorf("checking staged auth secrets: %w", err)
		}
	}

	sides, ok := reconcile.ResolveSides(in, f.Gateway)
	if !ok || !sides.Moving {
		return f, nil
	}
	f.Groups, err = gatherReversibleGroups(ctx, p, sides)
	return f, err
}

func stagedSecretsOfBothDomains(gw *reconcile.GatewayConfig, in reconcile.ReversibleInput) []string {
	set := map[string]struct{}{}
	for _, d := range []string{in.SourceDomain, in.TargetDomain} {
		for _, n := range reconcile.ResolveStagedSecretNames(gw, d) {
			set[n] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// gatherReversibleGroups reads every group and its committed topics on the
// side the route is bound to, and every group's state and type on the side
// being entered. In a back-out those are one side, read once. Committed topics
// are not read through a credential that may not see every group or topic: the
// core refuses on that gap, and a filtered read could turn the refusal into an
// error.
func gatherReversibleGroups(ctx context.Context, p ReversibleProviders, s reconcile.Sides) (*reconcile.ReversibleGroupFacts, error) {
	g := &reconcile.ReversibleGroupFacts{}
	bound := p.groups(s.Bound)
	canGroups, err := bound.CanDescribeAnyGroup(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking the %s credential's group access: %w", s.Bound, err)
	}
	canTopics, err := bound.CanDescribeAnyTopic(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking the %s credential's topic access: %w", s.Bound, err)
	}
	g.ListingIncomplete = reversibleGroupGap(s.Bound, canGroups)
	if g.ListingIncomplete == "" {
		g.ListingIncomplete = reversibleTopicGap(s.Bound, canTopics)
	}
	boundListing, err := groupoffsets.ListGroups(ctx, string(s.Bound), bound.ListGroups)
	if err != nil {
		return nil, fmt.Errorf("listing %s consumer groups: %w", s.Bound, err)
	}
	if g.ListingIncomplete == "" {
		ids := make([]string, 0, len(boundListing))
		for _, l := range boundListing {
			ids = append(ids, l.GroupID)
		}
		fetchCtx, span := tracing.Start(ctx, tracerName, "migplan.committed_topics")
		g.Committed, err = bound.CommittedTopics(fetchCtx, ids)
		tracing.End(span, err)
		if err != nil {
			return nil, fmt.Errorf("fetching committed offsets of %s consumer groups: %w", s.Bound, err)
		}
	}

	entered, enteredGap := boundListing, reversibleGroupGap(s.Bound, canGroups)
	if s.Writable != s.Bound {
		w := p.groups(s.Writable)
		can, err := w.CanDescribeAnyGroup(ctx)
		if err != nil {
			return nil, fmt.Errorf("checking the %s credential's group access: %w", s.Writable, err)
		}
		enteredGap = reversibleGroupGap(s.Writable, can)
		if entered, err = groupoffsets.ListGroups(ctx, string(s.Writable), w.ListGroups); err != nil {
			return nil, fmt.Errorf("listing %s consumer groups: %w", s.Writable, err)
		}
	}
	g.EnteredListingIncomplete = enteredGap
	g.EnteredStates, g.EnteredTypes = listingStates(entered)
	return g, nil
}

func reversibleGroupGap(s reconcile.Side, canDescribe bool) string {
	if canDescribe {
		return ""
	}
	return fmt.Sprintf("the %s credential cannot describe arbitrary consumer groups, so its group listing returns only the groups it can describe individually and the offsets of the rest cannot be read; the migration's group checks would miss them. Grant it DESCRIBE on all consumer groups (a group ACL on '*') and retry", s)
}

func reversibleTopicGap(s reconcile.Side, canDescribe bool) string {
	if canDescribe {
		return ""
	}
	return fmt.Sprintf("the %s credential cannot describe arbitrary topics, so offsets on the topics it cannot see are silently left out and the migration's group checks cannot see them. Grant it DESCRIBE on all topics (a topic ACL on '*') and retry", s)
}

// RunReversible gathers the facts, plans the migration, and attaches the CR the
// plan was computed against and its generation, the baseline the state machine
// checks before its point of no return.
func RunReversible(ctx context.Context, in reconcile.ReversibleInput, p ReversibleProviders) (*reconcile.Plan, error) {
	gatherCtx, span := tracing.Start(ctx, tracerName, "migplan.gather_facts")
	f, err := GatherReversibleFacts(gatherCtx, in, p)
	tracing.End(span, err)
	if err != nil {
		return nil, err
	}
	plan := reconcile.ReconcileReversibleStatic(in, f)
	// As on the static path: the skipped secrets check is also a warning, so a
	// check that never ran is never read as a pass.
	if f.SecretCheckSkipped != "" {
		plan.Report.Warnings = append(plan.Report.Warnings, f.SecretCheckSkipped)
	}
	if f.Gateway != nil {
		plan.GatewayYAML = f.Gateway.RawYAML
		plan.GatewayGeneration = f.Gateway.Generation
	}
	return plan, nil
}
