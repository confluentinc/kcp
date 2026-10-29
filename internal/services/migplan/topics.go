package migplan

import (
	"context"
	"sort"
)

var _ TopicLister = (*KafkaTopicLister)(nil)

// topicListerAdmin is the narrow slice of internal/client.KafkaAdmin that the
// topic lister needs: list topic names with the broker's internal flag, and
// read the cluster's own id. The real client.KafkaAdmin satisfies it directly.
type topicListerAdmin interface {
	ListTopicInternalFlags(ctx context.Context) (map[string]bool, error)
	ClusterID(ctx context.Context) (string, error)
}

// KafkaTopicLister lists a cluster's (non-internal) topics via a Kafka admin.
// It implements TopicLister.
type KafkaTopicLister struct {
	admin topicListerAdmin
}

func NewKafkaTopicLister(admin topicListerAdmin) *KafkaTopicLister {
	return &KafkaTopicLister{admin: admin}
}

// ListTopics returns the cluster's non-internal topic names, sorted for
// deterministic downstream artifacts. Brokers return the Kafka-internal topics
// (__consumer_offsets, __transaction_state) on an all-topics metadata request,
// so they are dropped here by the broker's own IsInternal flag, never by a name
// prefix (an operator topic such as _schemas is a user topic).
func (l *KafkaTopicLister) ListTopics(ctx context.Context) ([]string, error) {
	flags, err := l.admin.ListTopicInternalFlags(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(flags))
	for name, internal := range flags {
		if internal {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// ClusterID returns the cluster's own Kafka cluster id (from broker metadata).
func (l *KafkaTopicLister) ClusterID(ctx context.Context) (string, error) {
	return l.admin.ClusterID(ctx)
}
