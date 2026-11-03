package migplan

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

// ConvertProviders are the live seams a route conversion's facts are read
// through. Reconcile builds them once per run; the d2s state machine's
// verify_fence reads through the same ones after the fence.
type ConvertProviders struct {
	Gateway      GatewayConfigSource
	Source       TopicLister
	Target       TopicLister
	Link         LinkStatusProvider
	SourceGroups GroupLister
	TargetGroups GroupLister
}

// ConvertFacts is everything a route conversion's checks read from the live
// world except the source groups and their committed offsets: reconcile lists
// the source groups and fetches their committed topics itself, and
// verify_fence takes them from its own first snapshot. A denied probe is a
// fact here (the core refuses on it), never an error.
type ConvertFacts struct {
	Gateway      *reconcile.GatewayConfig
	SourceTopics []string
	TargetTopics []string
	Link         *LinkStatus
	IDs          reconcile.ClusterIDs

	// The five credential probes.
	SourceCanDescribeGroups bool
	TargetCanDescribeGroups bool
	SourceCanDescribeTopics bool
	TargetCanDescribeTopics bool
	TargetCanCommitOffsets  bool

	// TargetGroupListing is the destination's strict group listing, for the
	// split-brain check.
	TargetGroupListing []types.ConsumerGroupListing
	// Partitions is each link topic's partition count on both clusters. It is
	// read only when the four DESCRIBE probes allowed: the core refuses on a
	// denied probe before it reads counts, and a credential that may not
	// describe a topic would turn this read into an error instead of that
	// refusal. Both maps are nil when it was not read.
	Partitions reconcile.PartitionCounts
}

// describeAllowed reports whether every DESCRIBE probe allowed.
func (f *ConvertFacts) describeAllowed() bool {
	return f.SourceCanDescribeGroups && f.TargetCanDescribeGroups && f.SourceCanDescribeTopics && f.TargetCanDescribeTopics
}

// GroupFacts folds the facts and the source groups' committed topics (tracked;
// nil when only the credential checks will read the result) into the plain
// data the conversion's core checks read.
func (f *ConvertFacts) GroupFacts(tracked map[string][]string) reconcile.GroupFacts {
	return groupFacts(f.TargetGroupListing, tracked, listingGaps{
		sourceGroups: listingGap("source", f.SourceCanDescribeGroups),
		targetGroups: listingGap("destination", f.TargetCanDescribeGroups),
		sourceTopics: topicGap("source", f.SourceCanDescribeTopics),
		targetTopics: topicGap("destination", f.TargetCanDescribeTopics),
		targetCommit: commitGap(f.TargetCanCommitOffsets),
	})
}

// GatherConvertFacts reads a route conversion's facts: the gateway CR, both
// clusters' topic lists and ids, the cluster link's status, the five credential
// probes (the offset-write probe on the destination only), the destination's
// group listing, and the link topics' partition counts on both clusters. It
// gathers and never judges: any read failure is an error, so a partial answer
// can't be mistaken for a clean one, and a denied probe is returned as a fact.
// Reconcile and the d2s state machine's verify_fence both call it.
func GatherConvertFacts(ctx context.Context, p ConvertProviders) (*ConvertFacts, error) {
	if p.SourceGroups == nil || p.TargetGroups == nil {
		return nil, fmt.Errorf("a route conversion needs consumer-group listers for both clusters")
	}
	gw, err := p.Gateway.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading gateway config: %w", err)
	}
	src, err := p.Source.ListTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing source topics: %w", err)
	}
	tgt, err := p.Target.ListTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing target topics: %w", err)
	}
	link, err := p.Link.LinkStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading cluster-link status: %w", err)
	}
	srcID, err := p.Source.ClusterID(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading source cluster id: %w", err)
	}
	tgtID, err := p.Target.ClusterID(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading target cluster id: %w", err)
	}
	f := &ConvertFacts{
		Gateway:      gw,
		SourceTopics: src,
		TargetTopics: tgt,
		Link:         link,
		IDs:          reconcile.ClusterIDs{Source: srcID, Target: tgtID, LinkSource: link.SourceClusterID},
	}

	// Probed before any listing is trusted: a credential that may not describe
	// arbitrary groups or topics gets silently filtered answers, not an error.
	if f.SourceCanDescribeGroups, err = p.SourceGroups.CanDescribeAnyGroup(ctx); err != nil {
		return nil, fmt.Errorf("checking the source credential's group access: %w", err)
	}
	if f.TargetCanDescribeGroups, err = p.TargetGroups.CanDescribeAnyGroup(ctx); err != nil {
		return nil, fmt.Errorf("checking the destination credential's group access: %w", err)
	}
	if f.SourceCanDescribeTopics, err = p.SourceGroups.CanDescribeAnyTopic(ctx); err != nil {
		return nil, fmt.Errorf("checking the source credential's topic access: %w", err)
	}
	if f.TargetCanDescribeTopics, err = p.TargetGroups.CanDescribeAnyTopic(ctx); err != nil {
		return nil, fmt.Errorf("checking the destination credential's topic access: %w", err)
	}
	if f.TargetCanCommitOffsets, err = p.TargetGroups.CanCommitAnyOffsets(ctx); err != nil {
		return nil, fmt.Errorf("checking the destination credential's offset-write access: %w", err)
	}
	if f.TargetGroupListing, err = p.TargetGroups.ListGroups(ctx); err != nil {
		return nil, fmt.Errorf("listing destination consumer groups: %w", err)
	}

	if f.describeAllowed() {
		srcNames, tgtNames := linkTopicNames(link.LinkMirrors)
		if f.Partitions.Source, err = p.SourceGroups.PartitionCounts(ctx, srcNames); err != nil {
			return nil, fmt.Errorf("reading link topics' partition counts on the source: %w", err)
		}
		if f.Partitions.Target, err = p.TargetGroups.PartitionCounts(ctx, tgtNames); err != nil {
			return nil, fmt.Errorf("reading link topics' partition counts on the destination: %w", err)
		}
	}
	return f, nil
}

// linkTopicNames splits the link's mirrors into the names to count on each
// cluster: source names on the source, mirror names on the destination.
func linkTopicNames(mirrors []reconcile.LinkMirror) (source, target []string) {
	for _, m := range mirrors {
		source = append(source, m.SourceTopic)
		target = append(target, m.MirrorTopic)
	}
	return source, target
}
