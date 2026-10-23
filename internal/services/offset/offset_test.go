package offset

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/retry"
	"github.com/confluentinc/kcp/internal/retry/retrytest"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
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
	retrytest.Fast(t)

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

// newCutCluster is newMockCluster with the broker behind a listener the test can cut.
func newCutCluster(t *testing.T, topic string) (*testsupport.CutListener, *sarama.MockBroker, sarama.Client) {
	t.Helper()
	ln := testsupport.NewCutListener(t)
	broker := sarama.NewMockBrokerListener(t, 1, ln)
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).SetController(broker.BrokerID()).SetLeader(topic, 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).SetOffset(topic, 0, sarama.OffsetNewest, 4242),
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
	return ln, broker, client
}

// The reason for closing the broker before a retry: sarama keeps failing every request on a connection the
// broker reset, even once the broker is back, until the broker is closed.
func TestSarama_ABrokenConnectionStaysBrokenUntilTheBrokerIsClosed(t *testing.T) {
	const topic = "cut-topic"
	ln, _, client := newCutCluster(t, topic)
	leader, err := client.Leader(topic, 0)
	if err != nil {
		t.Fatal(err)
	}
	req := &sarama.OffsetRequest{Version: 1}
	req.AddBlock(topic, 0, sarama.OffsetNewest, 1)
	if _, err := leader.GetAvailableOffsets(req); err != nil {
		t.Fatalf("first request: %v", err)
	}

	ln.Cut()

	for i := 1; i <= 2; i++ {
		if _, err := leader.GetAvailableOffsets(req); err == nil {
			t.Fatalf("request %d after the reset succeeded on the broken connection", i)
		}
	}
	_ = leader.Close()
	leader, err = client.Leader(topic, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leader.GetAvailableOffsets(req); err != nil {
		t.Fatalf("after closing the broker, a request must reconnect: %v", err)
	}
}

// On this single-broker cluster sarama's own metadata refresh also closes the broker that fails it, so the test
// guards the end-to-end recovery; ClosesTheResetLeaderSoTheFirstRetryRecovers proves the close.
func TestGetMany_RecoversWhenTheLeaderResetsItsConnection(t *testing.T) {
	retrytest.Fast(t)
	const topic = "cut-topic"
	ln, broker, client := newCutCluster(t, topic)
	svc := NewOffsetService(client)
	if _, err := svc.GetMany(t.Context(), []string{topic}); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	ln.Cut()

	got, err := svc.GetMany(t.Context(), []string{topic})
	if err != nil {
		t.Fatalf("sweep after the reset: %v", err)
	}
	if got[topic][0] != 4242 {
		t.Fatalf("offset = %d, want 4242", got[topic][0])
	}
	if n := countOffsetRequests(broker); n != 2 {
		t.Fatalf("ListOffsets requests reaching the broker = %d, want 2 (one per sweep)", n)
	}
}

func TestGetMany_GivesUpAtTheBudgetOnATopicThatIsGone(t *testing.T) {
	retrytest.Fast(t)
	const topic = "gone-topic"
	// The leader answers UNKNOWN_TOPIC_OR_PARTITION for the partition, as it does once the topic is deleted.
	gone := &sarama.OffsetResponse{Version: 1}
	gone.AddTopicPartition(topic, 0, -1)
	gone.Blocks[topic][0].Err = sarama.ErrUnknownTopicOrPartition
	broker := sarama.NewMockBroker(t, 1)
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).SetController(broker.BrokerID()).SetLeader(topic, 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockWrapper(gone),
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	client, err := sarama.NewClient([]string{broker.Addr()}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); broker.Close() })

	_, err = NewOffsetService(client).GetMany(t.Context(), []string{topic})

	var gave *retry.GaveUpError
	if !errors.As(err, &gave) {
		t.Fatalf("err = %v, want a *retry.GaveUpError", err)
	}
	if !errors.Is(err, sarama.ErrUnknownTopicOrPartition) || !strings.Contains(err.Error(), topic+"/0") {
		t.Fatalf("err = %v, want it to name %s/0 and UNKNOWN_TOPIC_OR_PARTITION", err, topic)
	}
}

// The partition leader sits behind a listener the test cuts, among several healthy brokers that also answer
// metadata. sarama refreshes metadata through an arbitrary idle broker, so only closing the reset leader before
// the retry makes the first retry succeed in every run.
func TestGetMany_ClosesTheResetLeaderSoTheFirstRetryRecovers(t *testing.T) {
	retrytest.Fast(t)
	rec := tracingtest.Install(t)
	const topic = "cut-topic"
	const healthy = 8
	ln := testsupport.NewCutListener(t)
	leader := sarama.NewMockBrokerListener(t, 1, ln)
	brokers := []*sarama.MockBroker{leader}
	for i := 0; i < healthy; i++ {
		brokers = append(brokers, sarama.NewMockBroker(t, int32(i+2)))
	}
	for _, b := range brokers {
		meta := sarama.NewMockMetadataResponse(t).SetController(leader.BrokerID()).SetLeader(topic, 0, leader.BrokerID())
		for _, o := range brokers {
			meta.SetBroker(o.Addr(), o.BrokerID())
		}
		b.SetHandlerByMap(map[string]sarama.MockResponse{
			"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
			"MetadataRequest":    meta,
			"OffsetRequest":      sarama.NewMockOffsetResponse(t).SetOffset(topic, 0, sarama.OffsetNewest, 4242),
		})
	}
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	client, err := sarama.NewClient([]string{brokers[1].Addr()}, cfg)
	if err != nil {
		t.Fatalf("failed to create client against mock broker: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		for _, b := range brokers {
			b.Close()
		}
	})
	svc := NewOffsetService(client)
	if _, err := svc.GetMany(t.Context(), []string{topic}); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	ln.Cut()

	got, err := svc.GetMany(t.Context(), []string{topic})
	if err != nil {
		t.Fatalf("sweep after the reset: %v", err)
	}
	if got[topic][0] != 4242 {
		t.Fatalf("offset = %d, want 4242", got[topic][0])
	}
	sweeps := tracingtest.FindAll(tracingtest.Spans(rec), "offset.get_many")
	if len(sweeps) != 2 {
		t.Fatalf("offset.get_many spans = %d, want 2", len(sweeps))
	}
	attempts, ok := tracingtest.Attr(sweeps[1], "kcp.retry.attempts")
	if !ok || attempts.AsInt64() != 2 {
		t.Fatalf("kcp.retry.attempts = %v (set: %v), want 2: the first retry must recover", attempts, ok)
	}
}

func TestRetryableUnread_RetriesOnlyStaleMetadataAndConnectionErrors(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	tp := topicPartition{topic: "t", partition: 0}
	failedWith := func(causes ...error) []failedPartition {
		var out []failedPartition
		for _, c := range causes {
			out = append(out, failedPartition{tp: tp, cause: c})
		}
		return out
	}
	for name, tc := range map[string]struct {
		failed []failedPartition
		want   bool
	}{
		"a reset connection": {failedWith(fmt.Errorf("offset request to broker b failed for t/0: %w", reset)), true},
		"a stale-metadata block error": {
			failedWith(fmt.Errorf("offset error for t/0: %w", sarama.ErrNotLeaderForPartition)), true},
		"a leader lookup that found no leader": {
			failedWith(fmt.Errorf("failed to get leader for t/0: %w", sarama.ErrLeaderNotAvailable)), true},
		"a failed login": {
			failedWith(fmt.Errorf("offset request to broker b failed for t/0: %w", sarama.ErrSASLAuthenticationFailed)), false},
		"a TLS error": {
			failedWith(fmt.Errorf("offset request to broker b failed for t/0: %w", x509.UnknownAuthorityError{})), false},
		"a closed client on leader lookup": {
			failedWith(fmt.Errorf("failed to get leader for t/0: %w", sarama.ErrClosedClient)), false},
		"a leader lookup that ran out of brokers with no recorded cause": {
			failedWith(fmt.Errorf("failed to get leader for t/0: %w", sarama.ErrOutOfBrokers)), true},
		"a leader lookup that ran out of brokers, wrapped by sarama with no causes": {
			failedWith(fmt.Errorf("failed to get leader for t/0: %w", sarama.Wrap(sarama.ErrOutOfBrokers))), true},
		"a leader lookup that ran out of brokers on TLS errors": {
			failedWith(fmt.Errorf("failed to get leader for t/0: %w", sarama.Wrap(sarama.ErrOutOfBrokers, x509.UnknownAuthorityError{}))), false},
		"a reset and a failed login": {
			failedWith(fmt.Errorf("x: %w", reset), fmt.Errorf("y: %w", sarama.ErrSASLAuthenticationFailed)), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := retryableUnread(tc.failed); got != tc.want {
				t.Fatalf("retryableUnread = %v, want %v", got, tc.want)
			}
		})
	}
}

// newSASLCluster is a single-broker cluster whose SASL login succeeds for the client's bootstrap connection and
// fails for every later one, so the partition leader's connection fails to log in.
func newSASLCluster(t *testing.T, topic string) (*sarama.MockBroker, sarama.Client) {
	t.Helper()
	broker := sarama.NewMockBroker(t, 1)
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest":   sarama.NewMockApiVersionsResponse(t),
		"SaslHandshakeRequest": sarama.NewMockSaslHandshakeResponse(t).SetEnabledMechanisms([]string{sarama.SASLTypePlaintext}),
		"SaslAuthenticateRequest": sarama.NewMockSequence(
			sarama.NewMockSaslAuthenticateResponse(t),
			sarama.NewMockSaslAuthenticateResponse(t).SetError(sarama.ErrSASLAuthenticationFailed),
		),
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).SetController(broker.BrokerID()).SetLeader(topic, 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).SetOffset(topic, 0, sarama.OffsetNewest, 4242),
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	cfg.Net.SASL.Enable = true
	cfg.Net.SASL.Mechanism = sarama.SASLTypePlaintext
	cfg.Net.SASL.User = "u"
	cfg.Net.SASL.Password = "p"
	client, err := sarama.NewClient([]string{broker.Addr()}, cfg)
	if err != nil {
		t.Fatalf("failed to create client against mock broker: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		broker.Close()
	})
	return broker, client
}

func TestGetMany_ReturnsAFailedLoginAtOnce(t *testing.T) {
	retrytest.Fast(t)
	rec := tracingtest.Install(t)
	const topic = "sasl-topic"
	_, client := newSASLCluster(t, topic)

	_, err := NewOffsetService(client).GetMany(t.Context(), []string{topic})

	var gave *retry.GaveUpError
	if err == nil || errors.As(err, &gave) {
		t.Fatalf("err = %v, want the failed login returned without retries", err)
	}
	if !errors.Is(err, sarama.ErrSASLAuthenticationFailed) {
		t.Fatalf("err = %v, want it to wrap ErrSASLAuthenticationFailed", err)
	}
	span := tracingtest.Find(t, tracingtest.Spans(rec), "offset.get_many")
	if attempts, ok := tracingtest.Attr(span, "kcp.retry.attempts"); ok {
		t.Fatalf("kcp.retry.attempts = %v, want unset: a single attempt", attempts)
	}
}

// newBlockCluster is a single-broker cluster whose leader answers every ListOffsets with resp.
func newBlockCluster(t *testing.T, topic string, resp *sarama.OffsetResponse) (*sarama.MockBroker, sarama.Client) {
	t.Helper()
	broker := sarama.NewMockBroker(t, 1)
	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).SetController(broker.BrokerID()).SetLeader(topic, 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockWrapper(resp),
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	client, err := sarama.NewClient([]string{broker.Addr()}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); broker.Close() })
	return broker, client
}

func TestGetMany_ReturnsANonStaleBlockErrorAtOnce(t *testing.T) {
	retrytest.Fast(t)
	const topic = "denied-topic"
	denied := &sarama.OffsetResponse{Version: 1}
	denied.AddTopicPartition(topic, 0, -1)
	denied.Blocks[topic][0].Err = sarama.ErrTopicAuthorizationFailed
	broker, client := newBlockCluster(t, topic, denied)

	_, err := NewOffsetService(client).GetMany(t.Context(), []string{topic})

	var gave *retry.GaveUpError
	if err == nil || errors.As(err, &gave) {
		t.Fatalf("err = %v, want the block error returned without retries", err)
	}
	if !strings.Contains(err.Error(), sarama.ErrTopicAuthorizationFailed.Error()) {
		t.Fatalf("err = %v, want it to name TOPIC_AUTHORIZATION_FAILED", err)
	}
	if n := countOffsetRequests(broker); n != 1 {
		t.Fatalf("ListOffsets requests = %d, want 1", n)
	}
}

func TestGetMany_ReturnsAMissingBlockAtOnce(t *testing.T) {
	retrytest.Fast(t)
	const topic = "blockless-topic"
	broker, client := newBlockCluster(t, topic, &sarama.OffsetResponse{Version: 1})

	_, err := NewOffsetService(client).GetMany(t.Context(), []string{topic})

	var gave *retry.GaveUpError
	if err == nil || errors.As(err, &gave) {
		t.Fatalf("err = %v, want the missing block returned without retries", err)
	}
	if !strings.Contains(err.Error(), "no offset block returned for "+topic+"/0") {
		t.Fatalf("err = %v, want it to name the missing block", err)
	}
	if n := countOffsetRequests(broker); n != 1 {
		t.Fatalf("ListOffsets requests = %d, want 1", n)
	}
}

// outOfBrokersOnce is a client whose first leader lookup fails the way sarama's does when every broker was
// dropped and none recorded an error: a bare ErrOutOfBrokers.
type outOfBrokersOnce struct {
	sarama.Client
	failed bool
}

func (c *outOfBrokersOnce) Leader(topic string, partition int32) (*sarama.Broker, error) {
	if !c.failed {
		c.failed = true
		return nil, sarama.ErrOutOfBrokers
	}
	return c.Client.Leader(topic, partition)
}

func TestGetMany_RetriesALeaderLookupThatRanOutOfBrokers(t *testing.T) {
	retrytest.Fast(t)
	const topic = "cut-topic"
	_, broker, client := newCutCluster(t, topic)

	got, err := NewOffsetService(&outOfBrokersOnce{Client: client}).GetMany(t.Context(), []string{topic})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if got[topic][0] != 4242 {
		t.Fatalf("offset = %d, want 4242", got[topic][0])
	}
	if n := countOffsetRequests(broker); n != 1 {
		t.Fatalf("ListOffsets requests = %d, want 1 (sent by the retry)", n)
	}
}
