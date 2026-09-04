package providers

import (
	"context"
	"sort"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/migplan"
)

var _ migplan.TopicLister = (*KafkaTopicLister)(nil)

// topicListerAdmin is the narrow slice of internal/client.KafkaAdmin that the
// topic lister needs. Keeping it local means the unit test needs only a
// one-method fake, and the real client.KafkaAdmin satisfies it directly.
type topicListerAdmin interface {
	ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error)
}

// KafkaTopicLister lists a cluster's (non-internal) topics via a Kafka admin.
// It implements migplan.TopicLister.
type KafkaTopicLister struct {
	admin topicListerAdmin
}

func NewKafkaTopicLister(admin topicListerAdmin) *KafkaTopicLister {
	return &KafkaTopicLister{admin: admin}
}

// ListTopics returns the cluster's topic names, sorted for deterministic
// downstream artifacts. Internal topics are already excluded by the admin's
// listing.
func (l *KafkaTopicLister) ListTopics(_ context.Context) ([]string, error) {
	td, err := l.admin.ListTopicsWithConfigs()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(td))
	for name := range td {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
