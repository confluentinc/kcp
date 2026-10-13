package migplan

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/confluentinc/kcp/internal/client"
)

// fakeTopicAdmin's topics map is name -> the broker's IsInternal flag.
type fakeTopicAdmin struct {
	topics    map[string]bool
	clusterID string
	err       error
}

func (f *fakeTopicAdmin) ListTopicInternalFlags() (map[string]bool, error) {
	return f.topics, f.err
}

func (f *fakeTopicAdmin) GetClusterKafkaMetadata() (*client.ClusterKafkaMetadata, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &client.ClusterKafkaMetadata{ClusterID: f.clusterID}, nil
}

func TestKafkaTopicListerSorted(t *testing.T) {
	f := &fakeTopicAdmin{topics: map[string]bool{"c": false, "a": false, "b": false}}
	got, err := NewKafkaTopicLister(f).ListTopics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("ListTopics = %v, want sorted [a b c]", got)
	}
}

func TestKafkaTopicListerEmpty(t *testing.T) {
	f := &fakeTopicAdmin{topics: map[string]bool{}}
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

// The broker returns Kafka-internal topics (__consumer_offsets,
// __transaction_state) on an all-topics metadata request; the lister must drop
// every topic the broker flags internal, by flag rather than by name.
func TestKafkaTopicListerExcludesInternalTopics(t *testing.T) {
	f := &fakeTopicAdmin{topics: map[string]bool{
		"orders":              false,
		"__consumer_offsets":  true,
		"__transaction_state": true,
		"_schemas":            false, // underscore-prefixed but not broker-internal: kept
		"payments":            false,
	}}
	got, err := NewKafkaTopicLister(f).ListTopics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"_schemas", "orders", "payments"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListTopics = %v, want %v (internal topics excluded)", got, want)
	}
}
