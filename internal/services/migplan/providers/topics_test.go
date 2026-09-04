package providers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/IBM/sarama"
)

type fakeTopicAdmin struct {
	topics map[string]sarama.TopicDetail
	err    error
}

func (f *fakeTopicAdmin) ListTopicsWithConfigs() (map[string]sarama.TopicDetail, error) {
	return f.topics, f.err
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
