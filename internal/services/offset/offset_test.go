package offset

import (
	"fmt"
	"testing"
	"time"

	"github.com/IBM/sarama"
)

// newMockCluster starts a single MockBroker acting as controller and leader
// for numTopics single-partition topics named <prefix>0..<prefix>N-1, with
// the log end offset for each set to 100+i. The caller must Close the broker
// and client.
func newMockCluster(t *testing.T, numTopics int) (*sarama.MockBroker, sarama.Client, []string) {
	t.Helper()

	broker := sarama.NewMockBroker(t, 1)

	topics := make([]string, numTopics)
	metadata := sarama.NewMockMetadataResponse(t).
		SetBroker(broker.Addr(), broker.BrokerID()).
		SetController(broker.BrokerID())
	offsets := sarama.NewMockOffsetResponse(t)
	for i := range topics {
		topics[i] = fmt.Sprintf("bench-topic-%03d", i)
		metadata.SetLeader(topics[i], 0, broker.BrokerID())
		offsets.SetOffset(topics[i], 0, sarama.OffsetNewest, int64(100+i))
	}
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
		"MetadataRequest":    metadata,
		"OffsetRequest":      offsets,
	})

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	client, err := sarama.NewClient([]string{broker.Addr()}, cfg)
	if err != nil {
		t.Fatalf("failed to create client against mock broker: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		broker.Close()
	})
	return broker, client, topics
}

// countOffsetRequests counts ListOffsets requests in the mock broker history.
func countOffsetRequests(broker *sarama.MockBroker) int {
	count := 0
	for _, rr := range broker.History() {
		if _, ok := rr.Request.(*sarama.OffsetRequest); ok {
			count++
		}
	}
	return count
}

func TestGetMany_OffsetsMatchPerTopicGet(t *testing.T) {
	_, client, topics := newMockCluster(t, 25)
	svc := NewOffsetService(client)

	batched, err := svc.GetMany(t.Context(), topics)
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(batched) != len(topics) {
		t.Fatalf("GetMany returned %d topics, want %d", len(batched), len(topics))
	}

	for _, topic := range topics {
		single, err := svc.Get(t.Context(), topic)
		if err != nil {
			t.Fatalf("Get(%s): %v", topic, err)
		}
		if len(single) != len(batched[topic]) {
			t.Fatalf("topic %s: Get returned %d partitions, GetMany %d", topic, len(single), len(batched[topic]))
		}
		for p, off := range single {
			if batched[topic][p] != off {
				t.Errorf("topic %s partition %d: Get=%d GetMany=%d", topic, p, off, batched[topic][p])
			}
		}
	}
}

func TestGetMany_OneRequestPerBroker(t *testing.T) {
	broker, client, topics := newMockCluster(t, 50)
	svc := NewOffsetService(client)

	if _, err := svc.GetMany(t.Context(), topics); err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if got := countOffsetRequests(broker); got != 1 {
		t.Errorf("GetMany over 50 topics sent %d ListOffsets requests, want 1", got)
	}

	// The per-topic sweep the workflow used to do: one request per topic.
	// Kept as a contrast so a regression back to per-topic fetching in
	// GetMany is caught by the assertion above, not just slower in e2e.
	before := countOffsetRequests(broker)
	for _, topic := range topics {
		if _, err := svc.Get(t.Context(), topic); err != nil {
			t.Fatalf("Get(%s): %v", topic, err)
		}
	}
	if got := countOffsetRequests(broker) - before; got != len(topics) {
		t.Errorf("per-topic sweep sent %d ListOffsets requests, want %d", got, len(topics))
	}
}

func TestGetMany_Empty(t *testing.T) {
	_, client, _ := newMockCluster(t, 1)
	svc := NewOffsetService(client)

	got, err := svc.GetMany(t.Context(), nil)
	if err != nil {
		t.Fatalf("GetMany(nil): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("GetMany(nil) returned %d topics, want 0", len(got))
	}
}

func TestGetMany_UnknownTopic(t *testing.T) {
	_, client, topics := newMockCluster(t, 3)
	svc := NewOffsetService(client)

	_, err := svc.GetMany(t.Context(), append(topics, "no-such-topic"))
	if err == nil {
		t.Fatal("GetMany with unknown topic: want error, got nil")
	}
}

func TestSortedPartitionIDs(t *testing.T) {
	src := map[int32]int64{0: 100, 2: 200, 4: 400}
	dst := map[int32]int64{1: 50, 2: 150, 3: 300}

	got := SortedPartitionIDs(src, dst)
	want := []int32{0, 1, 2, 3, 4}

	if len(got) != len(want) {
		t.Fatalf("SortedPartitionIDs: got %d IDs, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SortedPartitionIDs[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestSortedPartitionIDs_Empty(t *testing.T) {
	got := SortedPartitionIDs(nil, nil)
	if len(got) != 0 {
		t.Fatalf("SortedPartitionIDs(nil, nil): got %d IDs, want 0", len(got))
	}
}

func TestComputeTotalLag(t *testing.T) {
	src := map[int32]int64{0: 1000, 1: 2000, 2: 3000}
	dst := map[int32]int64{0: 900, 1: 2000, 2: 2500}

	got := ComputeTotalLag(src, dst)
	// partition 0: 1000-900=100, partition 1: 0, partition 2: 3000-2500=500
	var want int64 = 600

	if got != want {
		t.Errorf("ComputeTotalLag = %d, want %d", got, want)
	}
}

func TestComputeTotalLag_MissingDestPartition(t *testing.T) {
	src := map[int32]int64{0: 1000, 1: 2000, 2: 3000}
	dst := map[int32]int64{0: 1000}

	got := ComputeTotalLag(src, dst)
	// partition 0: 0, partition 1: 2000 (missing), partition 2: 3000 (missing)
	var want int64 = 5000

	if got != want {
		t.Errorf("ComputeTotalLag = %d, want %d", got, want)
	}
}

func TestComputeTotalLag_DstAhead(t *testing.T) {
	// Edge case: destination is ahead of source (should not add negative lag)
	src := map[int32]int64{0: 100}
	dst := map[int32]int64{0: 200}

	got := ComputeTotalLag(src, dst)
	var want int64 = 0

	if got != want {
		t.Errorf("ComputeTotalLag (dst ahead) = %d, want %d", got, want)
	}
}

// TestGetMany_DeadLeaderBrokerRetried simulates the cached leader broker being
// unreachable (restarting/gone): the first metadata response names a dead
// broker as leader, so the offset request fails at the transport level. The
// refresh-and-retry pass must re-route the partition to the live leader and
// succeed rather than failing the sweep.
func TestGetMany_DeadLeaderBrokerRetried(t *testing.T) {
	// Shrink the retry backoff so the test doesn't pay the production 500ms.
	oldBackoff := retryBackoff
	retryBackoff = 5 * time.Millisecond
	t.Cleanup(func() { retryBackoff = oldBackoff })

	live := sarama.NewMockBroker(t, 1)

	// A broker that is registered in metadata but not listening: reserve a
	// real port with a MockBroker, then close it so connections are refused.
	dead := sarama.NewMockBroker(t, 2)
	deadAddr := dead.Addr()
	deadID := dead.BrokerID()
	dead.Close()

	const topic = "transport-retry-topic"

	metaDeadLeader := sarama.NewMockMetadataResponse(t).
		SetBroker(live.Addr(), live.BrokerID()).
		SetBroker(deadAddr, deadID).
		SetController(live.BrokerID()).
		SetLeader(topic, 0, deadID)
	metaLiveLeader := sarama.NewMockMetadataResponse(t).
		SetBroker(live.Addr(), live.BrokerID()).
		SetController(live.BrokerID()).
		SetLeader(topic, 0, live.BrokerID())

	live.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
		// The first metadata response (client bootstrap) points the leader at
		// the dead broker; every later one (GetMany's RefreshMetadata during
		// the retry pass) points at the live broker.
		"MetadataRequest": sarama.NewMockSequence(metaDeadLeader, metaLiveLeader),
		"OffsetRequest":   sarama.NewMockOffsetResponse(t).SetOffset(topic, 0, sarama.OffsetNewest, 4242),
	})

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	client, err := sarama.NewClient([]string{live.Addr()}, cfg)
	if err != nil {
		t.Fatalf("failed to create client against mock broker: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		live.Close()
	})

	svc := NewOffsetService(client)
	got, err := svc.GetMany(t.Context(), []string{topic})
	if err != nil {
		t.Fatalf("GetMany with dead leader broker: %v", err)
	}
	if got[topic][0] != 4242 {
		t.Fatalf("offset = %d, want 4242", got[topic][0])
	}
	// Exactly one ListOffsets must reach the live broker: the post-refresh
	// retry. The first attempt died on the dead broker's socket, proving the
	// transport-error path (not a lucky first route) was exercised.
	if n := countOffsetRequests(live); n != 1 {
		t.Errorf("live broker received %d ListOffsets requests, want 1 (the post-refresh retry)", n)
	}
}
