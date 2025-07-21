package mocks

import (
	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
)

// MockKafkaAdmin is a mock implementation of the KafkaAdmin interface
type MockKafkaAdmin struct {
	ListTopicsFunc              func() (map[string]sarama.TopicDetail, error)
	GetClusterKafkaMetadataFunc func() (*client.ClusterKafkaMetadata, error)
	DescribeConfigFunc          func() ([]sarama.ConfigEntry, error)
	CloseFunc                   func() error
}

func (m *MockKafkaAdmin) ListTopics() (map[string]sarama.TopicDetail, error) {
	return m.ListTopicsFunc()
}

func (m *MockKafkaAdmin) GetClusterKafkaMetadata() (*client.ClusterKafkaMetadata, error) {
	return m.GetClusterKafkaMetadataFunc()
}

func (m *MockKafkaAdmin) DescribeConfig() ([]sarama.ConfigEntry, error) {
	return m.DescribeConfigFunc()
}

func (m *MockKafkaAdmin) Close() error {
	return m.CloseFunc()
}
