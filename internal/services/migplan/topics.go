package migplan

import (
	"context"
	"sort"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
)

var _ TopicLister = (*KafkaTopicLister)(nil)

// topicListerAdmin is the narrow slice of internal/client.KafkaAdmin that the
// topic lister needs: list topics, and read the cluster's own id. The real
// client.KafkaAdmin satisfies it directly.
type topicListerAdmin interface {
	ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error)
	GetClusterKafkaMetadata() (*client.ClusterKafkaMetadata, error)
}

// KafkaTopicLister lists a cluster's (non-internal) topics via a Kafka admin.
// It implements TopicLister.
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

// ClusterID returns the cluster's own Kafka cluster id (from broker metadata).
func (l *KafkaTopicLister) ClusterID(_ context.Context) (string, error) {
	md, err := l.admin.GetClusterKafkaMetadata()
	if err != nil {
		return "", err
	}
	return md.ClusterID, nil
}
