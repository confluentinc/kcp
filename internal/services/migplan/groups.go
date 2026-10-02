package migplan

import (
	"context"
	"fmt"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

// GroupLister lists every consumer group on one cluster with its state, and the
// topics groups have committed offsets on. Only a route conversion uses it, for
// the split-brain check and to scope the convergence check, so an
// implementation must fail rather than return a partial result. Only the
// source's CommittedTopics is called.
type GroupLister interface {
	ListGroups(ctx context.Context) ([]types.ConsumerGroupListing, error)
	CommittedTopics(ctx context.Context, groups []string) (map[string][]string, error)
	// ClusterDescribeAccess reports whether the listing credential holds
	// DESCRIBE on the cluster. Without it ListGroups silently returns only the
	// groups the credential can describe individually.
	ClusterDescribeAccess(ctx context.Context) (client.DescribeAccess, error)
}

// strictGroupClient is the slice of client.ConsumerGroupClient the lister
// needs; the real client satisfies it.
type strictGroupClient interface {
	ListGroupsAllBrokers() ([]types.ConsumerGroupListing, error)
	CommittedTopics(groups []string) (map[string][]string, error)
	ClusterDescribeAccess() (client.DescribeAccess, error)
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

func (l *KafkaGroupLister) ClusterDescribeAccess(context.Context) (client.DescribeAccess, error) {
	return l.client.ClusterDescribeAccess()
}

// listingGap turns a listing credential's cluster access into the reason a
// conversion must refuse, or "" when the listing is complete. side is "source"
// or "destination". Both a denied and an unverifiable answer refuse. A hidden
// source group is a topic the convergence check never verifies; a hidden
// destination group is a split-brain the check cannot see.
func listingGap(side string, access client.DescribeAccess) string {
	switch access {
	case client.DescribeGranted:
		return ""
	case client.DescribeDenied:
		return fmt.Sprintf("the %s credential lacks DESCRIBE on the cluster, so its group listing returns only the groups it can describe individually and the conversion's group checks would miss the rest; grant cluster DESCRIBE (or use a credential that has it) and retry", side)
	default:
		return fmt.Sprintf("kcp could not determine whether the %s credential holds DESCRIBE on the cluster (the broker did not report its authorized operations), so a complete group listing cannot be confirmed; use a broker version that reports them or a credential known to hold cluster DESCRIBE", side)
	}
}

// groupFacts folds the two listings and the source's committed topics into the
// plain data ReconcileConvert reads.
func groupFacts(source, target []types.ConsumerGroupListing, tracked map[string][]string, sourceGap, targetGap string) reconcile.GroupFacts {
	f := reconcile.GroupFacts{
		TargetStates:            make(map[string]string, len(target)),
		TrackedTopics:           tracked,
		SourceListingIncomplete: sourceGap,
		TargetListingIncomplete: targetGap,
	}
	for _, l := range source {
		f.SourceGroups = append(f.SourceGroups, l.GroupID)
	}
	for _, l := range target {
		f.TargetStates[l.GroupID] = l.State
	}
	return f
}
