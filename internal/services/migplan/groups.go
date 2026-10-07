package migplan

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

// GroupLister is the conversion-only view of one cluster: every consumer group
// with its state, the topics groups have committed offsets on, the
// visibility probes, and topic partition counts. Only a route conversion uses
// it, so an implementation must fail rather than return a partial result. Only
// the source's CommittedTopics is called.
type GroupLister interface {
	ListGroups(ctx context.Context) ([]types.ConsumerGroupListing, error)
	CommittedTopics(ctx context.Context, groups []string) (map[string][]string, error)
	// CanDescribeAnyGroup reports whether the listing credential may describe an
	// arbitrary consumer group. Without it ListGroups silently returns only the
	// groups the credential can describe individually, and the offsets of the
	// rest can't be read.
	CanDescribeAnyGroup(ctx context.Context) (bool, error)
	// CanDescribeAnyTopic reports whether the credential may describe an
	// arbitrary topic. Without it the offset fetch silently leaves out the
	// topics it can't see, so a group committing outside the link could pass
	// the group rule.
	CanDescribeAnyTopic(ctx context.Context) (bool, error)
	// PartitionCounts returns the partition count of each named topic that
	// exists; a missing topic is absent from the map. It never creates a topic.
	PartitionCounts(ctx context.Context, topics []string) (map[string]int, error)
}

// strictGroupClient is the slice of client.ConsumerGroupClient the lister
// needs; the real client satisfies it.
type strictGroupClient interface {
	ListGroupsAllBrokers() ([]types.ConsumerGroupListing, error)
	CommittedTopics(groups []string) (map[string][]string, error)
	CanDescribeAnyGroup() (bool, error)
	CanDescribeAnyTopic() (bool, error)
	PartitionCounts(topics []string) (map[string]int, error)
}

var _ GroupLister = (*KafkaGroupLister)(nil)

// KafkaGroupLister lists groups through a consumer-group client's strict,
// every-broker listing.
type KafkaGroupLister struct {
	client strictGroupClient
}

func NewKafkaGroupLister(c strictGroupClient) *KafkaGroupLister {
	return &KafkaGroupLister{client: c}
}

func (l *KafkaGroupLister) ListGroups(context.Context) ([]types.ConsumerGroupListing, error) {
	return l.client.ListGroupsAllBrokers()
}

func (l *KafkaGroupLister) CommittedTopics(_ context.Context, groups []string) (map[string][]string, error) {
	return l.client.CommittedTopics(groups)
}

func (l *KafkaGroupLister) CanDescribeAnyGroup(context.Context) (bool, error) {
	return l.client.CanDescribeAnyGroup()
}

func (l *KafkaGroupLister) CanDescribeAnyTopic(context.Context) (bool, error) {
	return l.client.CanDescribeAnyTopic()
}

func (l *KafkaGroupLister) PartitionCounts(_ context.Context, topics []string) (map[string]int, error) {
	return l.client.PartitionCounts(topics)
}

// listingGap turns a listing credential's group-describe access into the reason a
// conversion must refuse, or "" when the listing is complete. side is "source" or
// "destination". A hidden source group is a group the group rule never sees;
// a hidden destination group is a split-brain the check cannot see.
func listingGap(side string, canDescribe bool) string {
	if canDescribe {
		return ""
	}
	return fmt.Sprintf("the %s credential cannot describe arbitrary consumer groups, so its group listing returns only the groups it can describe individually and the offsets of the rest cannot be read; the conversion's group checks would miss them. Grant it DESCRIBE on all consumer groups (a group ACL on '*'; with MSK IAM, the DescribeGroup action on every group resource) and retry", side)
}

// topicGap is listingGap for topics: the reason a conversion must refuse when a
// credential may not describe arbitrary topics, or "" when it may. A topic the
// credential can't see is left out of an offset fetch without an error, so a
// consumer group's commit on it never appears and the group rule can't see it.
func topicGap(side string, canDescribe bool) string {
	if canDescribe {
		return ""
	}
	return fmt.Sprintf("the %s credential cannot describe arbitrary topics, so offsets on the topics it cannot see are silently left out and the conversion would never verify them. Grant it DESCRIBE on all topics (a topic ACL on '*'; with MSK IAM, the DescribeTopic action on every topic resource) and retry", side)
}

// listingGaps holds the reason, per cluster, that a group listing or a topic view
// may be partial; an empty string means it is known to be complete.
type listingGaps struct {
	sourceGroups, targetGroups, sourceTopics, targetTopics string
}

// groupFacts folds the destination listing, the source groups' committed topics
// and the visibility gaps into the plain data ReconcileConvert reads.
func groupFacts(target []types.ConsumerGroupListing, tracked map[string][]string, gaps listingGaps) reconcile.GroupFacts {
	f := reconcile.GroupFacts{
		TargetStates:            make(map[string]string, len(target)),
		TrackedTopics:           tracked,
		SourceListingIncomplete: gaps.sourceGroups,
		TargetListingIncomplete: gaps.targetGroups,
		SourceTopicsIncomplete:  gaps.sourceTopics,
		TargetTopicsIncomplete:  gaps.targetTopics,
	}
	for _, l := range target {
		f.TargetStates[l.GroupID] = l.State
	}
	return f
}
