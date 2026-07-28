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
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// bootstrapAddr lists ALL THREE external listeners, and that is load-bearing
	// rather than belt-and-braces. The suite kills one broker mid-run; a client
	// bootstrapped only against that one could not refresh metadata afterwards,
	// so the reader's recovery would be untestable for a reason that has nothing
	// to do with the reader.
	bootstrapAddr = "localhost:29192,localhost:29193,localhost:29194"

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
	return []string{"localhost:29192", "localhost:29193", "localhost:29194"}
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

// The open-transaction fixture, left uncommitted until after the window closes.
//
// It exists for one assertion: KTD9's, that lag is measured against the last
// stable offset and not the high watermark. On a quiet cluster the two
// definitions agree and the difference cannot be observed at all, so the suite
// has to create the condition — a transaction that has written a transactional
// offset commit to __consumer_offsets and not yet been decided, which is the
// normal state on an exactly-once cluster and the reason a high-watermark-based
// lag would have a permanent floor.
const (
	openTxn   = fixturePrefix + "open-txn"
	openIn    = fixturePrefix + "open-in"
	openGroup = fixturePrefix + "open-cg"

	// openTxnTimeout has to outlast the rest of the observation window, or the
	// coordinator aborts the transaction, the last stable offset catches up to the
	// high watermark, and the condition under test disappears before the window
	// closes. It must stay under the broker's transaction.max.timeout.ms, which
	// docker-compose.yml sets to fifteen minutes.
	openTxnTimeout = 14 * time.Minute
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
	require.NoError(t, produceWithin(f, within),
		"could not produce a transaction within %s", within)
}

// produceWithin is produceTxnWithRetry without the *testing.T, for the phase
// produced on its own goroutine.
//
// t.Fatalf may only be called from the goroutine running the test, so the
// during-the-kill phase has to hand its failure back over a channel instead.
func produceWithin(f txnFixture, within time.Duration) error {
	deadline := time.Now().Add(within)
	var last error
	for time.Now().Before(deadline) {
		if err := produceTxn(f); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("gave up after %s; last error: %w", within, last)
}

// beginOpenTransaction leaves a transactional offset commit sitting in
// __consumer_offsets under a transaction that is never decided, and returns a
// function that abandons it.
//
// That record is what pins the partition's last stable offset below its high
// watermark for as long as the transaction is open, and that gap is the entire
// point: it is the condition under which a high-watermark-based lag can never
// reach zero. Without it, both definitions of lag report the same zero and the
// assertion below cannot tell them apart.
//
// It is written through the raw protocol rather than through a transactional
// producer because sarama's AddOffsetsToTxn only BUFFERS the offsets — nothing
// reaches __consumer_offsets until CommitTxn, and a committed transaction leaves
// no gap. So the four requests a commit would have issued are issued here, minus
// the EndTxn that would close it: InitProducerId to claim a producer id and
// epoch, AddOffsetsToTxn to put the group's offsets partition into the
// transaction, and TxnOffsetCommit to write the record.
func beginOpenTransaction(t *testing.T) func() {
	t.Helper()
	createTopics(t, openIn)

	client := newClient(t)
	id := openTxn

	coordinator, err := client.TransactionCoordinator(id)
	require.NoError(t, err, "failed to resolve the transaction coordinator for the deliberately-open transaction")

	// Versions chosen to match what sarama's own transaction manager sends at the
	// configured Kafka version, so this fixture speaks the same protocol the
	// committed fixtures do. A fresh producer id is claimed with -1/-1.
	init, err := coordinator.InitProducerID(&sarama.InitProducerIDRequest{
		Version:            4,
		TransactionalID:    &id,
		TransactionTimeout: openTxnTimeout,
		ProducerID:         -1,
		ProducerEpoch:      -1,
	})
	require.NoError(t, err, "InitProducerId failed for the deliberately-open transaction")
	require.Equal(t, sarama.ErrNoError, init.Err, "InitProducerId was refused: %v", init.Err)

	added, err := coordinator.AddOffsetsToTxn(&sarama.AddOffsetsToTxnRequest{
		Version:         2,
		TransactionalID: id,
		ProducerID:      init.ProducerID,
		ProducerEpoch:   init.ProducerEpoch,
		GroupID:         openGroup,
	})
	require.NoError(t, err, "AddOffsetsToTxn failed for the deliberately-open transaction")
	require.Equal(t, sarama.ErrNoError, added.Err, "AddOffsetsToTxn was refused: %v", added.Err)

	groupCoordinator, err := client.Coordinator(openGroup)
	require.NoError(t, err, "failed to resolve the group coordinator for the deliberately-open transaction")

	committed, err := groupCoordinator.TxnOffsetCommit(&sarama.TxnOffsetCommitRequest{
		Version:         2,
		TransactionalID: id,
		GroupID:         openGroup,
		ProducerID:      init.ProducerID,
		ProducerEpoch:   init.ProducerEpoch,
		Topics: map[string][]*sarama.PartitionOffsetMetadata{
			openIn: {{Partition: 0, Offset: 1, LeaderEpoch: -1}},
		},
	})
	require.NoError(t, err, "TxnOffsetCommit failed for the deliberately-open transaction")
	for topic, parts := range committed.Topics {
		for _, pe := range parts {
			require.Equal(t, sarama.ErrNoError, pe.Err,
				"the transactional offset commit was refused for %s[%d]: %v", topic, pe.Partition, pe.Err)
		}
	}

	// Deliberately no EndTxn here. That is the whole fixture.
	return func() {
		_, _ = coordinator.EndTxn(&sarama.EndTxnRequest{
			Version:           2,
			TransactionalID:   id,
			ProducerID:        init.ProducerID,
			ProducerEpoch:     init.ProducerEpoch,
			TransactionResult: false,
		})
	}
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

// newClient returns a plain sarama client, used for the two questions the admin
// API cannot answer: which broker coordinates a given transactional id, and
// where a partition's log currently starts.
func newClient(t *testing.T) sarama.Client {
	t.Helper()
	c, err := sarama.NewClient(bootstrapAddrs(), baseConfig())
	require.NoError(t, err, "failed to connect a client to %s", bootstrapAddr)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// txnStateLeaders maps each __transaction_state partition to the node currently
// leading it.
func txnStateLeaders(t *testing.T) map[int32]int32 {
	t.Helper()
	md, ok := internalTopicMetadata(t)
	require.True(t, ok, "could not describe the internal topics")
	out := map[int32]int32{}
	for _, tm := range md {
		if tm.Name != txnStateTopic {
			continue
		}
		for _, p := range tm.Partitions {
			out[p.ID] = p.Leader
		}
	}
	require.NotEmpty(t, out, "%s reports no partitions", txnStateTopic)
	return out
}

// pickTargetBroker chooses the node to kill: the one leading the most
// __transaction_state partitions, and the partitions it leads.
//
// Most, rather than any, so the kill disrupts as many fetch loops as the layout
// allows. Which node that is varies from run to run — leadership is assigned by
// the controller, not by this suite — so it is resolved from metadata rather
// than hardcoded, and reported in every failure message.
func pickTargetBroker(t *testing.T) (int32, []int32) {
	t.Helper()
	leaders := txnStateLeaders(t)
	led := map[int32][]int32{}
	for part, leader := range leaders {
		led[leader] = append(led[leader], part)
	}

	var best int32 = -1
	for broker, parts := range led {
		if best == -1 || len(parts) > len(led[best]) || (len(parts) == len(led[best]) && broker < best) {
			best = broker
		}
	}
	require.Contains(t, brokerContainer, best,
		"the busiest %s leader is node %d, which maps to no container", txnStateTopic, best)

	parts := append([]int32(nil), led[best]...)
	sort.Slice(parts, func(i, j int) bool { return parts[i] < parts[j] })
	require.NotEmpty(t, parts,
		"no node leads any %s partition, so killing one would disrupt no fetch loop", txnStateTopic)
	return best, parts
}

// pinnedTxnIDs finds want transactional ids whose transaction coordinator is
// broker, by asking the cluster rather than by reimplementing its hashing.
//
// This is what makes the ledger sharp instead of probabilistic. A transactional
// id's coordinator IS the leader of the __transaction_state partition its
// footprint is written to, so an id coordinated by the node about to be killed
// has its records land in a partition whose fetch loop must re-resolve a new
// leader to keep reading. Left to chance — three transactions over twelve
// partitions on three nodes — roughly a third of them would land on an
// undisturbed partition, and a reader that recovered on none of them could
// still satisfy the ledger.
//
// The salt in the name is what makes the search deterministic within a run: the
// ids are enumerated in order and reported in every failure message.
func pinnedTxnIDs(t *testing.T, client sarama.Client, base string, want int, broker int32) []string {
	t.Helper()
	var out []string
	for salt := 0; len(out) < want && salt < 500; salt++ {
		id := fmt.Sprintf("%s-c%d-txn", base, salt)
		b, err := client.TransactionCoordinator(id)
		if err != nil {
			continue
		}
		if b.ID() == broker {
			out = append(out, id)
		}
	}
	require.Len(t, out, want,
		"could not find %d transactional ids coordinated by node %d after 500 candidates", want, broker)
	return out
}

// earliestOffsets reads each partition's current log start offset.
//
// The continuity check needs this: the reader is assigned %s from EARLIEST, so
// its final next offset minus the log start is the span of offsets it was
// responsible for, and the number of records it read must account for all of
// them.
func earliestOffsets(t *testing.T, topic string) map[int32]int64 {
	t.Helper()
	client := newClient(t)
	require.NoError(t, client.RefreshMetadata(topic), "failed to refresh metadata for the offset scan")
	parts, err := client.Partitions(topic)
	require.NoError(t, err, "failed to list partitions for the offset scan")
	out := make(map[int32]int64, len(parts))
	for _, p := range parts {
		off, err := client.GetOffset(topic, p, sarama.OffsetOldest)
		require.NoError(t, err, "failed to read the log start offset of partition %d", p)
		out[p] = off
	}
	return out
}

// killBroker removes a node from the cluster without letting it hand over.
//
// `docker kill`, not `docker stop`: SIGTERM triggers a controlled shutdown, in
// which the broker asks the controller to move its leaderships before it goes
// and closes its connections tidily. That is the easy case. SIGKILL is the one
// worth testing — the socket simply stops answering, the controller has to
// notice through the broker session timeout, and the reader's cached connection
// is left latched on an error with no Kafka error code to classify.
func killBroker(t *testing.T, broker int32) {
	t.Helper()
	container, ok := brokerContainer[broker]
	require.True(t, ok, "node %d maps to no container", broker)
	out, err := exec.Command("docker", "kill", container).CombinedOutput()
	require.NoError(t, err, "failed to kill the broker leading the target partitions: %s", out)
}

// awaitLeadershipMovedOff blocks until no __transaction_state or
// __consumer_offsets partition is still led by broker, and every partition has a
// leader again. It returns the __transaction_state leadership afterwards.
//
// Both topics, not just the one under test: the reader is assigned partitions
// from both, and one of the other topic's partitions left without a leader would
// stall a fetch loop and fail the liveness assertion for an unrelated reason.
func awaitLeadershipMovedOff(t *testing.T, broker int32) map[int32]int32 {
	t.Helper()
	var last string
	require.Eventually(t, func() bool {
		md, ok := internalTopicMetadata(t)
		if !ok {
			last = "the internal topics are not describable"
			return false
		}
		for _, tm := range md {
			for _, p := range tm.Partitions {
				if p.Leader == broker {
					last = fmt.Sprintf("%s partition %d is still led by node %d", tm.Name, p.ID, broker)
					return false
				}
				if p.Leader < 0 {
					last = fmt.Sprintf("%s partition %d has no leader", tm.Name, p.ID)
					return false
				}
			}
		}
		return true
	}, 2*time.Minute, 2*time.Second,
		"leadership never moved off the killed node %d, so the cluster itself did not recover and the reader cannot be judged: %s", broker, last)
	return txnStateLeaders(t)
}

// ---------------------------------------------------------------------------
// Running the binary under test
// ---------------------------------------------------------------------------

// repoRoot resolves the checkout root from this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	require.NoError(t, err)
	return root
}

// kcpBinary returns the path to the binary the Makefile target built.
func kcpBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "kcp")
	_, err := os.Stat(path)
	require.NoError(t, err, "the kcp binary is missing — run `make test-txn-discovery-ha`, which builds it")
	return path
}

// artifact filenames, fixed so every run's assertions look in the same place.
const (
	outFile   = "txn-discovery.yaml"
	statsFile = "txn-discovery-stats.json"
	auditFile = "txn-discovery-audit.jsonl"
	logFile   = "kcp.log"
)

// kcpProc is a discovery run in flight.
type kcpProc struct {
	cmd    *exec.Cmd
	dir    string
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// startKCP launches `kcp migration txn-discovery` in its own directory, which is
// where the artifacts and kcp.log land.
func startKCP(t *testing.T, args ...string) *kcpProc {
	t.Helper()
	dir := t.TempDir()

	full := append([]string{"migration", "txn-discovery"}, args...)
	cmd := exec.Command(kcpBinary(t), full...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start(), "failed to start the kcp binary")

	p := &kcpProc{cmd: cmd, dir: dir, stdout: &stdout, stderr: &stderr}
	t.Cleanup(func() {
		if p.cmd.ProcessState == nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
		}
	})
	return p
}

// running reports whether the observation window is still open.
//
// It is the guard that keeps a harness timing failure from being read as a
// reader failure: a window that closed before the post-failover phase was
// produced would leave that phase missing from the ledger, which is exactly what
// a reader that never recovered looks like.
func (p *kcpProc) running() bool { return p.cmd.ProcessState == nil }

// awaitObserving blocks until the run has begun reading the cluster.
func (p *kcpProc) awaitObserving(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(p.dir, logFile))
		if err == nil && strings.Contains(string(data), "starting transaction discovery") {
			time.Sleep(5 * time.Second)
			return
		}
		if p.cmd.ProcessState != nil {
			t.Fatalf("the run exited before it began observing:\n%s\n%s", p.stdout.String(), p.stderr.String())
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("the run never reported that it had started observing:\n%s\n%s", p.stdout.String(), p.stderr.String())
}

// wait blocks for the run to finish and collects everything it produced.
func (p *kcpProc) wait(t *testing.T) *runResult {
	t.Helper()
	err := p.cmd.Wait()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("failed to wait for the kcp binary: %v", err)
	}
	return collect(t, p.dir, code, p.stdout.String(), p.stderr.String())
}

// ---------------------------------------------------------------------------
// Artifacts
// ---------------------------------------------------------------------------

// discoveryDoc mirrors txn-discovery.yaml. It is redeclared here rather than
// imported: the suite is a consumer of the on-disk format, and sharing the
// producer's struct would let a field rename pass unnoticed.
type discoveryDoc struct {
	Groups               []discoveryGroup `yaml:"groups"`
	IndividualTopicCount int              `yaml:"individual_topic_count"`
	IndividualTopics     []string         `yaml:"individual_topics"`
}

type discoveryGroup struct {
	Name             string   `yaml:"name"`
	ReadProcessWrite bool     `yaml:"read_process_write"`
	Topics           []string `yaml:"topics"`
	TransactionalIDs []string `yaml:"transactional_ids"`
}

// statsDoc is the slice of txn-discovery-stats.json this suite reads. Unlike the
// single-node suite it needs the PER-PARTITION array as well as the aggregates:
// the continuity and liveness scenarios are about individual partitions, and an
// aggregate cannot say which partition stopped advancing.
type statsDoc struct {
	Tail struct {
		PartitionsAssigned int   `json:"partitions_assigned"`
		PartitionsRunning  int   `json:"partitions_running"`
		PartitionsStalled  int   `json:"partitions_stalled"`
		RecordsRead        int64 `json:"records_read"`
		Lag                int64 `json:"lag"`
		OpenTxnBacklog     int64 `json:"open_txn_backlog"`
		DecodeErrors       int64 `json:"decode_errors"`
		LeadershipErrors   int64 `json:"leadership_errors"`
		UnclassifiedErrors int64 `json:"unclassified_errors"`
		OffsetResets       int64 `json:"offset_resets"`
		TransportErrors    int64 `json:"transport_errors"`

		Partitions []statsPartition `json:"partitions"`
	} `json:"tail"`
}

type statsPartition struct {
	Topic            string `json:"topic"`
	Partition        int32  `json:"partition"`
	NextOffset       int64  `json:"next_offset"`
	LastStableOffset int64  `json:"last_stable_offset"`
	HighWaterMark    int64  `json:"high_water_mark"`
	Lag              int64  `json:"lag"`
	OpenTxnBacklog   int64  `json:"open_txn_backlog"`
	Running          bool   `json:"running"`
	Stalled          bool   `json:"stalled"`
	RecordsRead      int64  `json:"records_read"`
	AbortedBatches   int64  `json:"aborted_batches"`
	LeadershipErrors int64  `json:"leadership_errors"`
	TransportErrors  int64  `json:"transport_errors"`
	LastError        string `json:"last_error"`
}

// auditLine mirrors one line of txn-discovery-audit.jsonl.
type auditLine struct {
	Timestamp time.Time `json:"timestamp"`
	TxnID     string    `json:"transactional_id"`
	Source    string    `json:"source"`
	Added     []string  `json:"added"`
	Topics    []string  `json:"topics"`
}

// runResult is everything one run left behind.
type runResult struct {
	exitCode int
	stdout   string
	stderr   string

	doc   *discoveryDoc
	audit []auditLine
	stats *statsDoc

	// files is every name the run left in its directory. It is captured when the
	// run finishes rather than read on demand, because the directory is a
	// t.TempDir() belonging to whichever test first triggered the shared run and
	// is removed when THAT test returns.
	files []string
}

// collect reads a finished run's directory.
func collect(t *testing.T, dir string, code int, stdout, stderr string) *runResult {
	t.Helper()
	r := &runResult{exitCode: code, stdout: stdout, stderr: stderr}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "failed to list the run's directory")
	for _, e := range entries {
		r.files = append(r.files, e.Name())
	}

	if data, err := os.ReadFile(filepath.Join(dir, outFile)); err == nil {
		var doc discoveryDoc
		require.NoError(t, yaml.Unmarshal(data, &doc), "the discovery YAML did not parse:\n%s", data)
		r.doc = &doc
	}
	if data, err := os.ReadFile(filepath.Join(dir, auditFile)); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var al auditLine
			require.NoError(t, json.Unmarshal([]byte(line), &al), "an audit line did not parse: %s", line)
			r.audit = append(r.audit, al)
		}
		require.NoError(t, scanner.Err(), "failed to read the audit log")
	}
	if data, err := os.ReadFile(filepath.Join(dir, statsFile)); err == nil {
		var sd statsDoc
		require.NoError(t, json.Unmarshal(data, &sd), "the stats JSON did not parse:\n%s", data)
		r.stats = &sd
	}
	return r
}

// groupWith returns the group containing topic, or nil.
func (r *runResult) groupWith(topic string) *discoveryGroup {
	for i := range r.doc.Groups {
		for _, tp := range r.doc.Groups[i].Topics {
			if tp == topic {
				return &r.doc.Groups[i]
			}
		}
	}
	return nil
}

// auditForTxn returns every audit line for one transactional id.
func (r *runResult) auditForTxn(txnID string) []auditLine {
	var out []auditLine
	for _, l := range r.audit {
		if l.TxnID == txnID {
			out = append(out, l)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The failover run
// ---------------------------------------------------------------------------

const (
	// failoverWindow is the observation window. It has to cover the whole
	// choreography — three production phases, a broker kill, a leader election
	// and the reader's own capped backoff — plus a quiet tail long enough for the
	// reader to catch up to the last stable offset on every partition.
	failoverWindow = 240 * time.Second

	// phaseTxns is how many transactions each phase produces. Small on purpose:
	// every id is pinned to the coordinator being killed, so three is already an
	// exhaustive ledger over the disrupted partitions rather than a sample.
	phaseTxns = 3

	// postPhaseQuietTail is how much of the window must still be open after the
	// post-failover phase is produced. It is asserted rather than calculated:
	// the run staying alive this long after the last fixture is what proves the
	// reader was given time to see it.
	postPhaseQuietTail = 30 * time.Second

	// prePhaseSettle lets the pre-failover phase be read before the broker goes
	// away, so a failure afterwards is unambiguously about resumption.
	prePhaseSettle = 12 * time.Second

	// postKillSettle gives the reader room to re-resolve before it is judged on
	// records produced after the kill. The fetch loop's backoff is capped at five
	// seconds and a metadata refresh has to land behind it.
	postKillSettle = 15 * time.Second
)

// phase is one group of transactions and when it was produced relative to the
// kill.
type phase struct {
	// name is "pre", "mid" or "post" and appears in every failure message: which
	// phase went missing is the whole diagnosis.
	name     string
	fixtures []txnFixture
}

// failoverResult is the run plus everything about the failover needed to judge
// it.
type failoverResult struct {
	*runResult

	// killedBroker is the node whose container was killed mid-run.
	killedBroker int32
	// killedPartitions are the __transaction_state partitions it led at the
	// moment of the kill: the fetch loops that had to re-resolve.
	killedPartitions []int32
	// leadersAfter is __transaction_state leadership once the cluster settled.
	leadersAfter map[int32]int32

	phases []phase

	// txnStateEarliest is each __transaction_state partition's log start offset,
	// captured before the run began. It is the left-hand end of the offset span
	// the continuity check accounts for.
	txnStateEarliest map[int32]int64
}

var (
	failoverOnce sync.Once
	failover     *failoverResult
)

// failoverRun performs one observation window with a broker killed in the middle
// of it, and returns its artifacts.
//
// One window shared by every test, because each window costs its full
// --duration and the choreography inside it cannot be repeated cheaply. The
// scenarios are all assertions about the same run.
func failoverRun(t *testing.T) *failoverResult {
	t.Helper()
	failoverOnce.Do(func() { failover = doFailoverRun(t) })
	require.NotNil(t, failover, "the failover run did not complete; see the first failing test in this package")
	return failover
}

func doFailoverRun(t *testing.T) *failoverResult {
	t.Helper()

	ensureInternalTopics(t)
	awaitFullISR(t)

	client := newClient(t)
	target, ledParts := pickTargetBroker(t)
	t.Logf("target node %d leads %s partitions %v", target, txnStateTopic, ledParts)

	// Every transactional id in every phase is coordinated by the node about to
	// be killed, so all three phases exercise the same disrupted partitions and
	// only their timing differs.
	phases := make([]phase, 0, 3)
	for _, name := range []string{"pre", "mid", "post"} {
		ids := pinnedTxnIDs(t, client, fixturePrefix+name, phaseTxns, target)
		fixtures := make([]txnFixture, 0, len(ids))
		for i, id := range ids {
			base := fmt.Sprintf("%s%s-%d", fixturePrefix, name, i)
			fixtures = append(fixtures, txnFixture{TxnID: id, Produce: []string{base + "-a", base + "-b"}})
		}
		phases = append(phases, phase{name: name, fixtures: fixtures})
	}
	for _, ph := range phases {
		createTopics(t, topicsOf(ph.fixtures)...)
	}

	earliest := earliestOffsets(t, txnStateTopic)

	proc := startKCP(t,
		"--source-bootstrap", bootstrapAddr,
		"--use-unauthenticated-plaintext",
		"--duration", failoverWindow.String(),
		"--interval", "5s",
		"--out", outFile,
		"--stats-out", statsFile,
		"--audit-log-out", auditFile,
	)
	proc.awaitObserving(t)

	// Phase one: before the kill.
	for _, f := range phases[0].fixtures {
		produceTxnWithRetry(t, f, 60*time.Second)
	}
	time.Sleep(prePhaseSettle)

	// Phase two: across the kill. Started first so the kill lands while these
	// transactions are in flight, and run on its own goroutine because a
	// transactional producer whose coordinator disappears blocks until it can
	// find the new one.
	midErr := make(chan error, 1)
	go func() {
		for _, f := range phases[1].fixtures {
			if err := produceWithin(f, 90*time.Second); err != nil {
				midErr <- err
				return
			}
		}
		midErr <- nil
	}()

	killBroker(t, target)
	require.NoError(t, <-midErr,
		"the during-the-kill phase could not be produced at all, so the cluster rather than the reader failed")

	leadersAfter := awaitLeadershipMovedOff(t, target)
	time.Sleep(postKillSettle)

	// Phase three: strictly after the kill. This is the phase that separates a
	// recovered reader from a silently stalled one — everything before it is
	// already in the accumulator, which is in-memory and append-only, so a reader
	// that died at the kill would still satisfy a ledger built only from phases
	// one and two.
	for _, f := range phases[2].fixtures {
		produceTxnWithRetry(t, f, 90*time.Second)
	}

	require.True(t, proc.running(),
		"the observation window closed before the post-failover phase was produced: this is a harness timing failure, not a reader failure — raise failoverWindow")

	// Opened here so it is still undecided when the window closes and the tail
	// snapshot is taken. Its own recovery is not under test; what it provides is
	// the high-watermark-to-last-stable-offset gap the lag assertion needs in
	// order to be about anything.
	abandonOpen := beginOpenTransaction(t)
	t.Cleanup(abandonOpen)

	time.Sleep(postPhaseQuietTail)
	require.True(t, proc.running(),
		"the observation window closed less than %s after the post-failover phase was produced, so the reader was never given time to see it: this is a harness timing failure, not a reader failure — raise failoverWindow",
		postPhaseQuietTail)

	res := proc.wait(t)
	require.Equal(t, 0, res.exitCode, "the discovery run failed:\nSTDOUT\n%s\nSTDERR\n%s", res.stdout, res.stderr)
	require.NotNil(t, res.doc, "the run wrote no %s:\nSTDOUT\n%s", outFile, res.stdout)
	require.NotNil(t, res.stats, "the run wrote no %s, so the reader's own account of itself is unavailable", statsFile)

	return &failoverResult{
		runResult:        res,
		killedBroker:     target,
		killedPartitions: ledParts,
		leadersAfter:     leadersAfter,
		phases:           phases,
		txnStateEarliest: earliest,
	}
}

// topicsOf flattens the topics a set of fixtures produces to.
func topicsOf(fs []txnFixture) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Produce...)
	}
	return out
}

// describe renders the run for a failure message.
func (r *failoverResult) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "killed node %d, which led %s partitions %v\n", r.killedBroker, txnStateTopic, r.killedPartitions)
	fmt.Fprintf(&b, "exit=%d\n--- STDOUT ---\n%s", r.exitCode, r.stdout)
	if r.stderr != "" {
		fmt.Fprintf(&b, "--- STDERR ---\n%s", r.stderr)
	}
	t := r.stats.Tail
	fmt.Fprintf(&b, "--- TAIL ---\n  %d/%d live, %d stalled, %d read, lag %d, backlog %d\n",
		t.PartitionsRunning, t.PartitionsAssigned, t.PartitionsStalled, t.RecordsRead, t.Lag, t.OpenTxnBacklog)
	fmt.Fprintf(&b, "  errors: %d transport, %d leadership, %d unclassified, %d decode, %d offset reset\n",
		t.TransportErrors, t.LeadershipErrors, t.UnclassifiedErrors, t.DecodeErrors, t.OffsetResets)
	for _, p := range t.Partitions {
		if p.Stalled || !p.Running || p.Lag > 0 || p.LastError != "" {
			fmt.Fprintf(&b, "  %s[%d]: next %d, LSO %d, HWM %d, lag %d, read %d, running=%v stalled=%v err=%q\n",
				p.Topic, p.Partition, p.NextOffset, p.LastStableOffset, p.HighWaterMark, p.Lag, p.RecordsRead,
				p.Running, p.Stalled, p.LastError)
		}
	}
	fmt.Fprintf(&b, "--- PHASES ---\n")
	for _, ph := range r.phases {
		for _, f := range ph.fixtures {
			present := r.groupWith(f.Produce[0]) != nil
			fmt.Fprintf(&b, "  %s %s present=%v\n", ph.name, f.TxnID, present)
		}
	}
	return b.String()
}

// TestFailoverActuallyDisruptedTheReader is the non-vacuity gate for every
// scenario below it, and the reason they are worth asserting at all.
//
// Two things have to be true before a passing ledger means anything. The cluster
// must really have moved leadership off the killed node — otherwise nothing was
// re-resolved. And the reader must really have noticed: a run that sailed
// through without a single failed fetch never exercised recovery, so the ledger
// would be proving that a healthy reader reads, which is not R11.
//
// The error counters are used here and ONLY here. A passing failover run is
// EXPECTED to show transport or leadership errors — that is the recovery
// happening, not a fault — so they are a premise, never a health signal. What
// health looks like is asserted separately, on partition liveness.
func TestFailoverActuallyDisruptedTheReader(t *testing.T) {
	r := failoverRun(t)

	require.NotEmpty(t, r.killedPartitions,
		"the killed node led no %s partition, so no fetch loop had to re-resolve anything", txnStateTopic)
	for _, part := range r.killedPartitions {
		leader, ok := r.leadersAfter[part]
		require.True(t, ok, "%s partition %d vanished from metadata after the kill", txnStateTopic, part)
		require.NotEqual(t, r.killedBroker, leader,
			"%s partition %d is still led by the killed node %d, so its leader never moved", txnStateTopic, part, r.killedBroker)
	}

	disruptions := r.stats.Tail.TransportErrors + r.stats.Tail.LeadershipErrors
	require.Positive(t, disruptions,
		"killing node %d caused the reader no transport or leadership error, so it was never disrupted and every assertion in this file proves nothing\n%s",
		r.killedBroker, r.describe())
}

// TestEveryPhaseAppearsInTheGrouping is the ledger: every transaction produced
// before, during and after the failover is in the final grouping.
//
// It is a ledger rather than a spot check because the obvious assertions are all
// satisfiable by a reader that died at the kill. The accumulator is in-memory
// and append-only, so "nothing observed before the failover was lost" holds
// trivially for a reader that never resumed. Enumerating the phases is what
// makes the absence of one of them the thing that fails.
func TestEveryPhaseAppearsInTheGrouping(t *testing.T) {
	r := failoverRun(t)

	missing := map[string][]string{}
	for _, ph := range r.phases {
		for _, f := range ph.fixtures {
			if r.groupWith(f.Produce[0]) == nil {
				missing[ph.name] = append(missing[ph.name], f.TxnID)
			}
		}
	}
	require.Empty(t, missing,
		"transactions are missing from the output, by phase: %v\n%s", missing, r.describe())

	for _, ph := range r.phases {
		for _, f := range ph.fixtures {
			g := r.groupWith(f.Produce[0])
			assert.ElementsMatch(t, f.Produce, g.Topics,
				"the %s-failover transaction %s did not produce its own group of topics\n%s", ph.name, f.TxnID, r.describe())
			assert.Contains(t, g.TransactionalIDs, f.TxnID,
				"the group is not credited to the %s-failover transaction %s\n%s", ph.name, f.TxnID, r.describe())
		}
	}
}

// TestPostFailoverTransactionsAreObserved is the discriminator the whole suite
// exists for, called out on its own so a failure names it directly.
//
// Every other signal a stalled reader produces is ambiguous. It reports its
// partitions live, because a loop retrying a dead connection has not exited. It
// reports zero lag, because a partition that never fetched successfully holds a
// zero last stable offset. It reports no duplicate group, most easily of all,
// because it stopped. The one thing it cannot do is show a transaction that was
// produced after it stopped reading.
func TestPostFailoverTransactionsAreObserved(t *testing.T) {
	r := failoverRun(t)

	post := r.phases[len(r.phases)-1]
	require.Equal(t, "post", post.name, "the last phase is %q, not the post-failover one", post.name)
	require.NotEmpty(t, post.fixtures, "the post-failover phase produced nothing, so this test proves nothing")

	for _, f := range post.fixtures {
		g := r.groupWith(f.Produce[0])
		require.NotNil(t, g,
			"a transaction produced STRICTLY AFTER the failover is absent from the grouping: the reader did not resume.\n"+
				"Its transactional id is coordinated by the node that was killed, so its footprint was written to a %s "+
				"partition whose fetch loop had to re-resolve a new leader. A loop that kept retrying the dead broker "+
				"instead still reports itself running with zero lag, so this absence is the only signal that says so.\n%s",
			txnStateTopic, r.describe())
		assert.Contains(t, g.TransactionalIDs, f.TxnID,
			"the post-failover group is not credited to %s\n%s", f.TxnID, r.describe())
	}
}

// TestEveryAssignedPartitionIsLiveAndReadingAtTheEnd is R11's liveness half:
// no partition gave up and no partition is stuck retrying when the window closes.
//
// Both numbers are needed and neither is enough. A loop that exited stops being
// counted as running, which is the loud failure. A loop stuck retrying a dead
// connection is still counted as running, contributes no lag, and is otherwise
// indistinguishable from a healthy idle partition — that is the quiet one, and
// PartitionsStalled is the only signal that separates them.
//
// This asserts on liveness rather than on the error counters deliberately. The
// counters are expected to be positive on a passing run.
func TestEveryAssignedPartitionIsLiveAndReadingAtTheEnd(t *testing.T) {
	r := failoverRun(t)
	tl := r.stats.Tail

	require.Positive(t, tl.PartitionsAssigned,
		"the reader was assigned no partitions at all, so its liveness says nothing\n%s", r.describe())
	assert.Equal(t, tl.PartitionsAssigned, tl.PartitionsRunning,
		"%d of %d partitions stopped across the failover, so part of the window went unobserved\n%s",
		tl.PartitionsAssigned-tl.PartitionsRunning, tl.PartitionsAssigned, r.describe())
	assert.Zero(t, tl.PartitionsStalled,
		"%d partitions were still retrying a failed fetch when the window closed, so they never recovered from the failover — and their zero lag means nothing was read, not that they caught up\n%s",
		tl.PartitionsStalled, r.describe())

	for _, p := range tl.Partitions {
		assert.True(t, p.Running,
			"%s[%d] stopped early\n%s", p.Topic, p.Partition, r.describe())
		assert.False(t, p.Stalled,
			"%s[%d] is retrying rather than reading; its last error was %q\n%s",
			p.Topic, p.Partition, p.LastError, r.describe())
	}

	// Not a keep-up signal but a format-drift alarm, asserted here because a
	// failover is exactly when a reader might start misreading a batch header: a
	// new leader serves the same records from its own log, and a reader that
	// mishandled the boundary would decode rather than fail outright.
	assert.Zero(t, tl.DecodeErrors,
		"records failed to decode across the failover\n%s", r.describe())
}

// TestNoOffsetRangeSkippedAcrossTheResume is the continuity scenario: the reader
// resumed where it left off rather than jumping ahead.
//
// A skipped range is the quietest failure this component has. Every other signal
// reads clean — the partition is running, its offset advanced, its lag is zero,
// and the records it did read decoded fine — while the transactions in the gap
// are simply absent, which downstream is indistinguishable from a cluster on
// which those transactions never happened.
//
// It is derived from an identity the stats document makes checkable. The
// transaction-state log is assigned from EARLIEST, its records are written by the
// coordinator as ordinary non-transactional records so its offsets are dense, and
// the reader only ever advances past records it actually decoded. So for each of
// its partitions the number of records read must account for the ENTIRE offset
// span between the log start and where the reader finished. Anything less is a
// gap, whatever else the numbers say.
func TestNoOffsetRangeSkippedAcrossTheResume(t *testing.T) {
	r := failoverRun(t)

	// The left-hand end of every span has to still be where it was, or the
	// arithmetic below is measuring against the wrong origin. The compose file
	// disables the log cleaner precisely so this holds.
	now := earliestOffsets(t, txnStateTopic)
	for part, start := range r.txnStateEarliest {
		require.Equal(t, start, now[part],
			"the log start offset of %s partition %d moved from %d to %d during the run, so the offset span cannot be accounted for — has the log cleaner been re-enabled?",
			txnStateTopic, part, start, now[part])
	}

	killed := map[int32]bool{}
	for _, p := range r.killedPartitions {
		killed[p] = true
	}

	var totalSpan, killedSpan int64
	for _, p := range r.stats.Tail.Partitions {
		if p.Topic != txnStateTopic {
			// __consumer_offsets is assigned from LATEST, so its log start is not
			// where the reader began and the span identity does not apply.
			continue
		}
		start, ok := r.txnStateEarliest[p.Partition]
		require.True(t, ok, "%s partition %d has no captured log start offset", txnStateTopic, p.Partition)

		span := p.NextOffset - start
		require.GreaterOrEqual(t, span, int64(0),
			"%s[%d] finished at offset %d, behind its log start %d\n%s", p.Topic, p.Partition, p.NextOffset, start, r.describe())

		// The identity only holds while nothing was filtered out. The transaction
		// coordinator writes plain records, so a dropped batch here would mean the
		// reader is misreading the log rather than that the assertion is too
		// strict.
		require.Zero(t, p.AbortedBatches,
			"%s[%d] dropped %d batches as aborted, which the transaction-state log should never contain\n%s",
			p.Topic, p.Partition, p.AbortedBatches, r.describe())

		assert.Equal(t, span, p.RecordsRead,
			"%s[%d] read %d records over an offset span of %d (log start %d, finished at %d): %d offsets went unread, so a range was skipped rather than resumed\n%s",
			p.Topic, p.Partition, p.RecordsRead, span, start, p.NextOffset, span-p.RecordsRead, r.describe())

		totalSpan += span
		if killed[p.Partition] {
			killedSpan += span
		}
	}

	require.Positive(t, totalSpan,
		"the reader consumed no %s offsets at all, so continuity is vacuous\n%s", txnStateTopic, r.describe())
	// And specifically across the resume point: continuity over partitions that
	// were never disrupted would prove nothing about the failover.
	require.Positive(t, killedSpan,
		"no partition led by the killed node %d carried any record, so continuity was not checked across the resume point at all\n%s",
		r.killedBroker, r.describe())
}

// TestReconnectDuplicatedNoGroupAndNoAuditLine is the other half of resuming
// correctly: the reader did not re-read a range it had already consumed.
//
// Re-reading is the mirror image of skipping, and it is not harmless. A
// transaction observed twice would be credited twice, so a topic could appear in
// two groups — and an operator reading that would migrate the same topic in two
// separate batches, or conclude the tool cannot be trusted about either.
//
// The audit half is asserted as growth-only per resulting topic set rather than
// as one line per transaction. A transaction that produces to two topics may
// legitimately be observed first with one of them and then with both, because
// the Ongoing record is written as partitions are added — two genuine growth
// events. What must never happen is the SAME topic set being recorded twice,
// which is what a re-read range would produce.
func TestReconnectDuplicatedNoGroupAndNoAuditLine(t *testing.T) {
	r := failoverRun(t)

	seen := map[string]int{}
	for _, g := range r.doc.Groups {
		seen[g.Name]++
	}
	for name, n := range seen {
		assert.Equal(t, 1, n, "group name %s appears %d times in the document", name, n)
	}

	for _, ph := range r.phases {
		for _, f := range ph.fixtures {
			var in []string
			for _, g := range r.doc.Groups {
				for _, tp := range g.Topics {
					if tp == f.Produce[0] {
						in = append(in, g.Name)
					}
				}
			}
			assert.Len(t, in, 1,
				"the %s-failover transaction %s has its topic in groups %v, so the reconnect duplicated it\n%s",
				ph.name, f.TxnID, in, r.describe())
		}
	}

	require.NotEmpty(t, r.audit, "the run wrote no audit log at all, so duplication in it cannot be checked\n%s", r.describe())
	for _, ph := range r.phases {
		for _, f := range ph.fixtures {
			lines := r.auditForTxn(f.TxnID)
			require.NotEmpty(t, lines,
				"the %s-failover transaction %s has no audit line, so its grouping cannot be explained\n%s", ph.name, f.TxnID, r.describe())

			bySet := map[string]int{}
			for _, l := range lines {
				set := append([]string(nil), l.Topics...)
				sort.Strings(set)
				bySet[strings.Join(set, ",")]++
			}
			for set, n := range bySet {
				assert.Equal(t, 1, n,
					"the %s-failover transaction %s has %d audit lines recording the identical topic set {%s}, so the reconnect re-read a range it had already consumed\n%s",
					ph.name, f.TxnID, n, set, r.describe())
			}

			final := append([]string(nil), f.Produce...)
			sort.Strings(final)
			assert.Contains(t, bySet, strings.Join(final, ","),
				"no audit line records the %s-failover transaction %s reaching its full topic set\n%s", ph.name, f.TxnID, r.describe())
		}
	}
}

// TestLagRecoversToZeroAgainstTheLastStableOffset is the keep-up scenario, and
// KTD9 is the whole of its subtlety.
//
// Under ReadCommitted the broker serves nothing past the last stable offset, so
// measuring lag against the HIGH WATERMARK gives a figure with a permanent floor
// of the high-watermark-to-LSO gap whenever any transaction is open — which on
// __consumer_offsets is the normal state on an exactly-once cluster. A reader
// that had fully observed the window would report itself permanently behind, and
// operators would learn to ignore the one indicator that says the window was
// covered.
//
// So the test does two things a plain "lag is zero" would not. It checks the
// arithmetic from the artifact, that lag is the last-stable-offset gap and the
// high-watermark gap is reported separately. And it requires a partition to
// actually HAVE an open transaction at the end, because on a quiet cluster the
// two definitions agree and the distinction cannot be observed at all.
func TestLagRecoversToZeroAgainstTheLastStableOffset(t *testing.T) {
	r := failoverRun(t)
	tl := r.stats.Tail

	// Non-vacuity, and the confusion the stalled flag exists to prevent: a reader
	// that never fetched successfully holds a zero last stable offset, so its lag
	// computes as zero too.
	require.Positive(t, tl.RecordsRead,
		"the reader read no records at all, so zero lag would mean nothing was read rather than that it caught up\n%s", r.describe())

	assert.Zero(t, tl.Lag,
		"the reader never caught up to the last stable offset after the failover\n%s", r.describe())

	for _, p := range tl.Partitions {
		assert.Zero(t, p.Lag,
			"%s[%d] is %d records behind its last stable offset\n%s", p.Topic, p.Partition, p.Lag, r.describe())
		assert.Equal(t, max(int64(0), p.LastStableOffset-p.NextOffset), p.Lag,
			"%s[%d] reports lag %d, which is not its last-stable-offset gap (LSO %d, next %d)\n%s",
			p.Topic, p.Partition, p.Lag, p.LastStableOffset, p.NextOffset, r.describe())
		assert.Equal(t, max(int64(0), p.HighWaterMark-p.LastStableOffset), p.OpenTxnBacklog,
			"%s[%d] reports an open-transaction backlog of %d, which is not its high-watermark gap (HWM %d, LSO %d)\n%s",
			p.Topic, p.Partition, p.OpenTxnBacklog, p.HighWaterMark, p.LastStableOffset, r.describe())
	}

	var withOpenTxn []statsPartition
	for _, p := range tl.Partitions {
		if p.OpenTxnBacklog > 0 {
			withOpenTxn = append(withOpenTxn, p)
		}
	}
	require.NotEmpty(t, withOpenTxn,
		"no partition had an open transaction when the window closed, so an LSO-based lag and a high-watermark-based one would report the same zero and this test cannot tell them apart\n%s",
		r.describe())
	for _, p := range withOpenTxn {
		assert.Zero(t, p.Lag,
			"%s[%d] has an open transaction (HWM %d, LSO %d) and reports lag %d: the lag is being measured against the high watermark, so it can never reach zero while any transaction is open\n%s",
			p.Topic, p.Partition, p.HighWaterMark, p.LastStableOffset, p.Lag, r.describe())
	}
}
