package discovery

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fixture builders -------------------------------------------------------
//
// Keys are hand-assembled bytes against Kafka's OffsetCommitKey.json and
// GroupMetadataKey.json schemas rather than produced by an encoder from the
// package under test, so a decoder that agrees only with itself still fails.

func be16(v int16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(v))
	return b
}

func be32(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

// kstr encodes a classic (non-flexible) string: int16 length then bytes.
func kstr(s string) []byte {
	return append(be16(int16(len(s))), []byte(s)...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// commitKey builds an OffsetCommitKey v1 — the key an EOS app's
// sendOffsetsToTransaction writes for one consumed topic-partition.
func commitKey(group, topic string, partition int32) []byte {
	return concat(be16(1), kstr(group), kstr(topic), be32(partition))
}

// groupMetadataKey builds a GroupMetadataKey v2 — consumer-group state, which
// shares the __consumer_offsets topic with offset commits but names no topic.
func groupMetadataKey(group string) []byte {
	return concat(be16(2), kstr(group))
}

// commitBatch builds the batch a transactional offset commit arrives in: the
// header carries the producer id that ties it to its transaction.
func commitBatch(producerID int64, keys ...[]byte) tail.Batch {
	b := tail.Batch{
		Topic:           DefaultConsumerOffsetsTopic,
		ProducerID:      producerID,
		IsTransactional: true,
	}
	for i, k := range keys {
		b.Records = append(b.Records, tail.Record{
			Offset: int64(i),
			Key:    k,
			Value:  []byte{0, 1}, // a non-empty value: a live commit, not a tombstone
		})
	}
	return b
}

// flush runs the tail's final flush into a buffered channel and returns
// everything it emitted.
func flush(t *testing.T, tl *ConsumerOffsetsTail) []Observation {
	t.Helper()
	out := make(chan Observation, 256)
	tl.FinalFlush(out)
	close(out)
	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	return got
}

// --- tests ------------------------------------------------------------------

func TestATransactionalCommitWhoseProducerIDIsInTheCatalogResolvesToAnObservation(t *testing.T) {
	// The exact join this whole unit exists for: __transaction_state recorded that
	// producer 4242 is running transaction "payments-txn-0", and __consumer_offsets
	// recorded that producer 4242 committed an offset for "orders.in". Neither
	// record alone says the transaction consumes that topic; joining them on the
	// producer id does, with no assumption about how the names relate.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)

	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})
	tl.HandleBatch(commitBatch(4242, commitKey("payments-group", "orders.in", 0)))

	got := flush(t, tl)

	require.Len(t, got, 1)
	assert.Equal(t, "payments-txn-0", got[0].TxnID)
	assert.Equal(t, int64(4242), got[0].ProducerID)
	assert.Equal(t, []string{"orders.in"}, got[0].Topics)
	assert.True(t, got[0].ReadProcessWrite, "a recovered consumed input makes the transaction read-process-write")
	assert.Equal(t, SourceConsumerOffsets, got[0].Source)
	assert.False(t, got[0].ObservedAt.IsZero())
}

func TestANonTransactionalBatchIsIgnored(t *testing.T) {
	// The overwhelming majority of __consumer_offsets traffic is ordinary,
	// non-transactional offset commits. A batch header carries a producer id for
	// plain idempotent producers too, so correlating one would attribute an
	// unrelated consumer's topics to whichever transaction happens to share that
	// id. Only a commit written INSIDE a transaction says anything about one.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.IsTransactional = false
	tl.HandleBatch(b)

	assert.Empty(t, flush(t, tl), "a non-transactional commit must not correlate")
}

func TestAControlBatchIsIgnored(t *testing.T) {
	// A control batch is the transaction marker itself — the COMMIT or ABORT the
	// coordinator writes — not application data. The tail component delivers
	// control batches flagged rather than filtering them, so dropping them is
	// this consumer's job. A marker is transactional and carries a real producer
	// id, so every other filter here waves it through; only the flag stops it.
	// Its record key is not an OffsetCommitKey at all, so treating it as one
	// would decode a marker as a consumed topic.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.Control = true
	tl.HandleBatch(b)

	assert.Empty(t, flush(t, tl), "a control batch must not correlate")
}

func TestABatchWithANonPositiveProducerIDIsIgnored(t *testing.T) {
	// -1 is Kafka's "no producer id" sentinel and 0 is the zero value of an
	// absent field. Buffering either would key a pending entry on a sentinel, so
	// every unrelated producer lacking an id would pool its consumed topics into
	// one bucket — and the moment any transaction happened to be catalogued
	// under that sentinel, the whole pool would be attributed to it. The
	// catalog's own guard only refuses to WRITE a non-positive id; refusing to
	// READ one is this side's job.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	for _, pid := range []int64{-1, 0} {
		tl.HandleBatch(commitBatch(pid, commitKey("payments-group", "orders.in", 0)))
	}

	assert.Empty(t, flush(t, tl), "a sentinel producer id must not correlate")
	// And it is not merely unresolved — nothing was buffered under the sentinel
	// key at all, so a later catalog entry cannot resurrect it.
	assert.Empty(t, tl.resolveWith(map[int64]string{-1: "some-txn", 0: "other-txn"}, time.Now()),
		"nothing may be buffered under a sentinel producer id")
}

func TestABatchFromAnotherTopicOnTheSharedChannelIsIgnored(t *testing.T) {
	// One tail instance serves both this consumer and the __transaction_state
	// reader, fanning every partition of both topics into a single channel, so
	// each consumer must demultiplex on the batch's topic. The batch here is
	// transactional with a real producer id, so every other filter waves it
	// through — only the topic check stops a __transaction_state record's key
	// from being decoded as an OffsetCommitKey and published as a consumed topic.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.Topic = "__transaction_state"
	tl.HandleBatch(b)

	assert.Empty(t, flush(t, tl), "a batch from another topic must not correlate")
}

func TestTheDemultiplexedTopicFollowsTheConfiguredTopic(t *testing.T) {
	// The demux compares against a configured name, not a hardcoded one. U11
	// builds the tail's TopicSpec and this consumer from the same setting, so a
	// hardcode here would silently drop every batch on a cluster whose offsets
	// topic is named otherwise — a consumer that reads nothing looks exactly
	// like a cluster with no exactly-once traffic.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{Topic: "__custom_offsets"})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.Topic = "__custom_offsets"
	tl.HandleBatch(b)

	got := flush(t, tl)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"orders.in"}, got[0].Topics)
}

func TestAGroupMetadataKeyContributesNoConsumedTopic(t *testing.T) {
	// Key version 2 is GroupMetadataKey: consumer-group state sharing the
	// __consumer_offsets topic, naming no topic at all. It decodes successfully,
	// so nothing upstream rejects it — and its empty Topic field would otherwise
	// be buffered as a consumed topic named "", which reaches the YAML as a
	// nameless member of a real transaction's group.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	tl.HandleBatch(commitBatch(4242, groupMetadataKey("payments-group")))

	assert.Empty(t, flush(t, tl), "group metadata names no consumed topic")
}

func TestAnInternalConsumedTopicIsDropped(t *testing.T) {
	// An exactly-once app can commit offsets for an internal topic — a Streams
	// repartition or changelog topic is the routine case. Publishing one as a
	// recovered input would hand the grouping stage an edge through a topic
	// every such app shares, and the union-find would chain unrelated workloads
	// into one group. Grouping filters internal topics itself, but this source
	// also feeds the audit trail and the recovered-topics stat, both of which
	// would otherwise report a topic no operator can migrate.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	tl.HandleBatch(commitBatch(4242,
		commitKey("payments-group", "__consumer_offsets", 0),
		commitKey("payments-group", "app-repartition-store-changelog", 0),
		commitKey("payments-group", "orders.in", 0),
	))

	got := flush(t, tl)

	require.Len(t, got, 1)
	assert.Equal(t, []string{"app-repartition-store-changelog", "orders.in"}, got[0].Topics,
		"only __-prefixed topics are internal; a Streams changelog is a real topic")
}

func TestATombstonedCommitIsNotALiveConsumedInput(t *testing.T) {
	// A tombstone — a valid key with an empty value — is how the coordinator
	// DELETES an offset commit on expiry or an admin DeleteOffsets. Its key
	// still names a topic, so every decode-side check passes it; reading it as
	// a live commit would report an input the application no longer consumes,
	// and the operator would migrate a topic into a group that does not need it.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	b := commitBatch(4242, commitKey("payments-group", "orders.in", 0))
	b.Records[0].Value = nil
	tl.HandleBatch(b)

	assert.Empty(t, flush(t, tl), "a tombstone is a deleted commit, not a live one")
}

func TestAKeyThatFailsToDecodeIsCountedAndDoesNotStopTheTail(t *testing.T) {
	// Record keys are a broker-internal binary format, not part of the stable
	// client protocol, so a broker upgrade can change them under us. A decode
	// failure must neither panic nor abandon the rest of the batch — dropping
	// the remaining records would silently lose recoveries — and it must be
	// counted, because the counter is the only format-drift alarm this source
	// has. A run that decoded nothing otherwise looks like an idle cluster.
	cat := NewTxnCatalog()
	cat.Observe("payments-txn-0", 4242)
	tl := NewConsumerOffsetsTail(cat, ConsumerOffsetsOptions{})

	tl.HandleBatch(commitBatch(4242,
		be16(99),  // an unsupported key version
		[]byte{0}, // shorter than the version prefix itself
		commitKey("payments-group", "orders.in", 0),
	))

	got := flush(t, tl)

	require.Len(t, got, 1, "the records after a bad key are still processed")
	assert.Equal(t, []string{"orders.in"}, got[0].Topics)
	assert.Equal(t, int64(2), tl.Stats().KeyDecodeErrors)
}
