//go:build e2e_txndiscovery_ha

// Package txndiscoveryha_test exercises `kcp migration txn-discovery` across a
// partition leadership change, against a real three-broker Kafka cluster started
// by docker-compose.
//
// It is the leader-failover half of R11. The single-node suite
// (integration-tests/txn-discovery) proves the eight functional grouping
// scenarios and a whole-broker restart; this suite proves the narrower and
// quieter thing: that when the leader of a __transaction_state partition
// disappears mid-run, the reader re-resolves the new leader and keeps reading.
// It is also the only place the tool runs against a realistically-replicated
// internal topic.
//
// Per KTD5 the suite runs the BUILT BINARY as a subprocess and asserts on the
// files it wrote, exactly as the single-node suite does.
package txndiscoveryha_test

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/require"
)

const (
	// bootstrapAddr lists ALL THREE external listeners, and that is load-bearing
	// rather than belt-and-braces. The suite kills one broker mid-run; a client
	// bootstrapped only against that one could not refresh metadata afterwards,
	// so the reader's recovery would be untestable for a reason that has nothing
	// to do with the reader.
	bootstrapAddr = "localhost:29092,localhost:29093,localhost:29094"

	// txnStateTopic is the internal topic the command reads by default, and whose
	// partition leadership this suite moves.
	txnStateTopic = "__transaction_state"
	// offsetsTopic is the other topic the reader is assigned. It is named here
	// because the ISR and leadership gates have to cover both: a partition of
	// either one that never got a leader back would stall a fetch loop.
	offsetsTopic = "__consumer_offsets"

	// fixturePrefix is what every topic and transactional id this suite creates
	// carries, so a name cannot collide with anything the cluster made itself.
	fixturePrefix = "zzha-"

	// wantReplicas is the replication factor the compose file gives the internal
	// topics. Asserted rather than assumed: at RF 1 the partitions the killed
	// broker led would have no surviving replica to be re-elected from, and this
	// suite would be testing an unavailable partition instead of a moved leader.
	wantReplicas = 3
)

// brokerContainer maps a KRaft node id to the container running it, so a test can
// kill the broker leading a particular partition. The ids come from
// KAFKA_NODE_ID in docker-compose.yml.
var brokerContainer = map[int32]string{
	1: "kcp-test-txn-discovery-ha-kafka1",
	2: "kcp-test-txn-discovery-ha-kafka2",
	3: "kcp-test-txn-discovery-ha-kafka3",
}

// kafkaVersion is the brokers' protocol version. cp-kafka 7.6.0 is Kafka 3.6.
var kafkaVersion = sarama.V3_6_0_0

// baseConfig is the shared sarama configuration for every fixture client.
func baseConfig() *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.Version = kafkaVersion
	cfg.ClientID = "kcp-txn-discovery-ha-e2e"
	// Generous metadata retries: every fixture client in this suite may be asked
	// a question while a leader election is in flight.
	cfg.Metadata.Retry.Max = 20
	cfg.Metadata.Retry.Backoff = 500 * time.Millisecond
	return cfg
}

// newAdmin returns a cluster admin against the cluster.
//
// A fresh one per call, never a cached package-level client: this suite kills a
// broker, and a client built before the kill holds stale metadata and a dead
// connection, so reusing it would keep reporting the pre-kill view.
func newAdmin(t *testing.T) sarama.ClusterAdmin {
	t.Helper()
	admin, err := sarama.NewClusterAdmin(bootstrapAddrs(), baseConfig())
	require.NoError(t, err, "failed to connect a cluster admin to %s — is the compose cluster up?", bootstrapAddr)
	t.Cleanup(func() { _ = admin.Close() })
	return admin
}

// bootstrapAddrs splits the bootstrap string sarama wants as a slice.
func bootstrapAddrs() []string {
	return []string{"localhost:29092", "localhost:29093", "localhost:29094"}
}

// liveBrokerIDs returns the node ids the cluster currently reports, reporting
// failure rather than failing the test so it can be polled.
func liveBrokerIDs(t *testing.T) ([]int32, bool) {
	t.Helper()
	admin, err := sarama.NewClusterAdmin(bootstrapAddrs(), baseConfig())
	if err != nil {
		return nil, false
	}
	defer func() { _ = admin.Close() }()
	brokers, _, err := admin.DescribeCluster()
	if err != nil {
		return nil, false
	}
	ids := make([]int32, 0, len(brokers))
	for _, b := range brokers {
		ids = append(ids, b.ID())
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, true
}

// internalTopicMetadata describes both internal topics, reporting failure rather
// than failing the test so it can be polled.
func internalTopicMetadata(t *testing.T) ([]*sarama.TopicMetadata, bool) {
	t.Helper()
	admin, err := sarama.NewClusterAdmin(bootstrapAddrs(), baseConfig())
	if err != nil {
		return nil, false
	}
	defer func() { _ = admin.Close() }()
	md, err := admin.DescribeTopics([]string{txnStateTopic, offsetsTopic})
	if err != nil {
		return nil, false
	}
	for _, tm := range md {
		if tm.Err != sarama.ErrNoError {
			return nil, false
		}
		if len(tm.Partitions) == 0 {
			return nil, false
		}
	}
	return md, true
}

// describeISR renders the internal topics' replication state for a failure
// message: which partitions are short of a full ISR, and by how much.
func describeISR(md []*sarama.TopicMetadata) string {
	out := ""
	for _, tm := range md {
		short := 0
		for _, p := range tm.Partitions {
			if len(p.Isr) < wantReplicas || len(p.Replicas) < wantReplicas {
				short++
			}
		}
		out += fmt.Sprintf("  %s: %d partitions, %d short of %d in-sync replicas\n",
			tm.Name, len(tm.Partitions), short, wantReplicas)
	}
	return out
}

// TestClusterIsThreeBrokersWithReplicatedInternalTopics is the premise every
// other test in this file rests on, asserted rather than assumed.
//
// A failover test on a cluster whose __transaction_state is replicated once
// would not be testing failover: killing the leader of a single-replica
// partition makes that partition unavailable for the rest of the run, so the
// reader would be waiting on data that no longer exists anywhere rather than
// re-resolving a leader that moved. Both look like a stalled partition from the
// outside, and only one of them is R11.
func TestClusterIsThreeBrokersWithReplicatedInternalTopics(t *testing.T) {
	ids, ok := liveBrokerIDs(t)
	require.True(t, ok, "could not describe the cluster at %s", bootstrapAddr)
	require.Len(t, ids, 3, "the cluster reports %v, not three brokers — this suite needs a leader to be able to move", ids)
	for _, id := range ids {
		require.Contains(t, brokerContainer, id,
			"the cluster reports node id %d, which maps to no container: brokerContainer is out of step with docker-compose.yml", id)
	}

	ensureInternalTopics(t)
	awaitFullISR(t)

	md, ok := internalTopicMetadata(t)
	require.True(t, ok, "the internal topics are not both describable yet")
	for _, tm := range md {
		for _, p := range tm.Partitions {
			require.Len(t, p.Replicas, wantReplicas,
				"%s partition %d has %d replicas, want %d\n%s", tm.Name, p.ID, len(p.Replicas), wantReplicas, describeISR(md))
			require.Len(t, p.Isr, wantReplicas,
				"%s partition %d has %d in-sync replicas, want %d — killing a leader would take it below min-ISR\n%s",
				tm.Name, p.ID, len(p.Isr), wantReplicas, describeISR(md))
		}
	}
}

// ---------------------------------------------------------------------------
// Kafka fixture helpers
// ---------------------------------------------------------------------------

// Warm-up fixture names. Producing one full consume-transform-produce
// transaction is what brings BOTH internal topics into existence: the
// transaction coordinator creates __transaction_state on the first
// InitProducerId, and the group coordinator creates __consumer_offsets on the
// first offset commit. Neither exists on a fresh cluster.
const (
	warmupTxn   = fixturePrefix + "warmup-txn"
	warmupOut   = fixturePrefix + "warmup-out"
	warmupIn    = fixturePrefix + "warmup-in"
	warmupGroup = fixturePrefix + "warmup-cg"
)

// createTopics creates each topic at the cluster's replication factor, ignoring
// "already exists".
//
// Replication factor three, not one, and it matters as much as the internal
// topics' does: a single-replica fixture topic whose only replica sat on the
// broker this suite kills could never be produced to again, so the
// post-failover phase would be missing from the ledger for a reason that is not
// the reader. That is precisely the negative control's signature, which would
// make the negative control prove nothing.
//
// One partition each, so a transaction's footprint is a single growth event
// rather than one per partition added.
func createTopics(t *testing.T, names ...string) {
	t.Helper()
	admin := newAdmin(t)
	for _, name := range names {
		err := admin.CreateTopic(name, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: wantReplicas}, false)
		if err != nil && !errors.Is(err, sarama.ErrTopicAlreadyExists) &&
			!strings.Contains(err.Error(), "already exists") {
			t.Fatalf("failed to create topic: %v", err)
		}
	}
}

// txnFixture describes one transaction to produce.
type txnFixture struct {
	// TxnID is the transactional.id the transaction-state log records the
	// footprint under, and the key whose hash selects the __transaction_state
	// partition — and therefore the coordinating broker.
	TxnID string
	// Produce are the topics the transaction writes to. They become its
	// footprint and therefore its group.
	Produce []string
	// ConsumeTopic and Group, when set, make this a consume-transform-produce
	// transaction, committing the offsets inside the transaction.
	ConsumeTopic string
	Group        string
}

// produceTxn runs one transaction to completion, returning an error rather than
// failing the test.
//
// The error return is what the during-the-kill phase needs: a transactional
// producer whose commit fails is not recoverable — sarama puts the transaction
// into a fatal state — so retrying means building a new producer, which the
// caller decides to do, not this function.
func produceTxn(f txnFixture) error {
	cfg := baseConfig()
	cfg.Producer.Idempotent = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Return.Successes = true
	cfg.Producer.Return.Errors = true
	cfg.Net.MaxOpenRequests = 1
	cfg.Producer.Transaction.ID = f.TxnID
	cfg.Producer.Transaction.Timeout = 60 * time.Second

	producer, err := sarama.NewSyncProducer(bootstrapAddrs(), cfg)
	if err != nil {
		return fmt.Errorf("failed to create a transactional producer: %w", err)
	}
	defer func() { _ = producer.Close() }()

	if err := producer.BeginTxn(); err != nil {
		return fmt.Errorf("BeginTxn failed: %w", err)
	}
	for _, topic := range f.Produce {
		if _, _, err := producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Key:   sarama.StringEncoder("k"),
			Value: sarama.StringEncoder("v"),
		}); err != nil {
			return fmt.Errorf("failed to produce inside the transaction: %w", err)
		}
	}
	if f.ConsumeTopic != "" {
		if err := producer.AddOffsetsToTxn(map[string][]*sarama.PartitionOffsetMetadata{
			f.ConsumeTopic: {{Partition: 0, Offset: 1}},
		}, f.Group); err != nil {
			return fmt.Errorf("AddOffsetsToTxn failed: %w", err)
		}
	}
	if err := producer.CommitTxn(); err != nil {
		return fmt.Errorf("CommitTxn failed: %w", err)
	}
	return nil
}

// produceTxnWithRetry runs one transaction, rebuilding the producer on each
// attempt until the deadline.
//
// Rebuilding rather than retrying in place is required, not defensive: once a
// transactional producer's commit fails sarama marks it fatally errored and
// every later call on it fails too. A fresh producer re-runs InitProducerId,
// which fences the abandoned attempt and starts a new epoch — which is exactly
// what a real exactly-once client does when its coordinator moves.
func produceTxnWithRetry(t *testing.T, f txnFixture, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last error
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		if err := produceTxn(f); err == nil {
			return
		} else {
			last = err
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("could not produce a transaction within %s; last error: %v", within, last)
}

// ensureInternalTopics brings __transaction_state and __consumer_offsets into
// existence by running one full consume-transform-produce transaction.
//
// Neither exists on a fresh cluster: the coordinators create them on first use.
// Without them the command's preflight fails and the offsets tail reports itself
// unavailable — both for reasons that have nothing to do with failover.
func ensureInternalTopics(t *testing.T) {
	t.Helper()
	createTopics(t, warmupOut, warmupIn)
	produceTxnWithRetry(t, txnFixture{
		TxnID:        warmupTxn,
		Produce:      []string{warmupOut},
		ConsumeTopic: warmupIn,
		Group:        warmupGroup,
	}, 2*time.Minute)

	require.Eventually(t, func() bool { _, ok := internalTopicMetadata(t); return ok },
		2*time.Minute, 2*time.Second,
		"the coordinators did not create both internal topics after a full consume-transform-produce transaction")
}

// awaitFullISR blocks until every partition of both internal topics has all
// three replicas in sync.
//
// This gate is why the suite can kill a broker at all. A partition whose ISR is
// still catching up has only one replica eligible for leadership, so killing its
// leader leaves it offline rather than re-elected — and an offline partition and
// a reader that failed to re-resolve look identical from the artifacts.
func awaitFullISR(t *testing.T) {
	t.Helper()
	var last string
	ok := func() bool {
		md, ok := internalTopicMetadata(t)
		if !ok {
			last = "  the internal topics are not describable\n"
			return false
		}
		last = describeISR(md)
		for _, tm := range md {
			for _, p := range tm.Partitions {
				if len(p.Replicas) < wantReplicas || len(p.Isr) < wantReplicas {
					return false
				}
			}
		}
		return true
	}
	require.Eventually(t, ok, 3*time.Minute, 3*time.Second,
		"the internal topics never reached a full in-sync replica set, so a leader could not survive being killed:\n%s", last)
}
