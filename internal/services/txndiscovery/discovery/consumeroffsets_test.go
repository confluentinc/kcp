package discovery

import (
	"encoding/binary"
	"testing"

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
