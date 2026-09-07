package migplan

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
)

type fakeTopicAdmin struct {
	topics    map[string]sarama.TopicDetail
	clusterID string
	err       error
}

func (f *fakeTopicAdmin) ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error) {
	return f.topics, f.err
}

func (f *fakeTopicAdmin) GetClusterKafkaMetadata() (*client.ClusterKafkaMetadata, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &client.ClusterKafkaMetadata{ClusterID: f.clusterID}, nil
}

func TestKafkaTopicListerSorted(t *testing.T) {
	f := &fakeTopicAdmin{topics: map[string]sarama.TopicDetail{"c": {}, "a": {}, "b": {}}}
	got, err := NewKafkaTopicLister(f).ListTopics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("ListTopics = %v, want sorted [a b c]", got)
	}
}

func TestKafkaTopicListerEmpty(t *testing.T) {
	f := &fakeTopicAdmin{topics: map[string]sarama.TopicDetail{}}
	got, err := NewKafkaTopicLister(f).ListTopics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestKafkaTopicListerError(t *testing.T) {
	f := &fakeTopicAdmin{err: errors.New("admin boom")}
	if _, err := NewKafkaTopicLister(f).ListTopics(context.Background()); err == nil {
		t.Fatal("expected the admin error to propagate")
	}
}

func TestKafkaTopicListerClusterID(t *testing.T) {
	f := &fakeTopicAdmin{clusterID: "abc123"}
	got, err := NewKafkaTopicLister(f).ClusterID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc123" {
		t.Errorf("ClusterID = %q, want abc123", got)
	}
	// error propagates
	if _, err := NewKafkaTopicLister(&fakeTopicAdmin{err: errors.New("md boom")}).ClusterID(context.Background()); err == nil {
		t.Fatal("expected the metadata error to propagate")
	}
}
