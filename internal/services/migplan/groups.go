package migplan

import (
	"context"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

// GroupLister lists every consumer group on one cluster with its state. Only a
// route conversion uses it, for the split-brain check, so an implementation
// must fail rather than return a partial listing.
type GroupLister interface {
	ListGroups(ctx context.Context) ([]types.ConsumerGroupListing, error)
}

// strictGroupClient is the slice of client.ConsumerGroupClient the lister
// needs; the real client satisfies it.
type strictGroupClient interface {
	ListGroupsAllBrokers() ([]types.ConsumerGroupListing, error)
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

// groupFacts folds the two listings into the plain data ReconcileConvert reads.
func groupFacts(source, target []types.ConsumerGroupListing) reconcile.GroupFacts {
	f := reconcile.GroupFacts{TargetStates: make(map[string]string, len(target))}
	for _, l := range source {
		f.SourceGroups = append(f.SourceGroups, l.GroupID)
	}
	for _, l := range target {
		f.TargetStates[l.GroupID] = l.State
	}
	return f
}
