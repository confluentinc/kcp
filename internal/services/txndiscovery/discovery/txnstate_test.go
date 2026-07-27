package discovery

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
)

// --- literal-byte fixtures ---
//
// The __transaction_state key and value bytes are hand-assembled rather than built
// with an encoder, for the same reason the txnlog decoder's own tests are: a fixture
// produced by the code under test would only prove the reader agrees with itself.

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

func be64(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

// kstr encodes a classic (non-flexible) string: int16 length, then the bytes.
func kstr(s string) []byte { return append(be16(int16(len(s))), s...) }

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// txnKey encodes a __transaction_state record key at schema v0.
func txnKey(txnID string) []byte {
	return concat(be16(0), kstr(txnID))
}

// txnValue encodes a __transaction_state record value at schema v0 (the classic,
// non-flexible encoding), with one partition per named topic.
func txnValue(producerID int64, status int8, topics ...string) []byte {
	parts := [][]byte{
		be16(0),          // version = 0
		be64(producerID), // producerId
		be16(1),          // producerEpoch
		be32(60000),      // timeoutMs
		{byte(status)},   // transactionStatus
		be32(int32(len(topics))),
	}
	for _, tp := range topics {
		parts = append(parts,
			kstr(tp),
			be32(1), // one partition
			be32(0), // partition 0
		)
	}
	parts = append(parts, be64(1000), be64(900)) // lastUpdateMs, startMs
	return concat(parts...)
}

// stateBatch wraps records into the batch shape the tail delivers for the
// transaction-state topic.
func stateBatch(records ...tail.Record) tail.Batch {
	return tail.Batch{Topic: DefaultTxnStateTopic, Partition: 3, Records: records}
}

// runReader feeds the reader one closed channel of batches and returns everything it
// emitted. Feeding a closed channel keeps the test synchronous: Run returns when the
// input is drained, so there is no goroutine to join and no timeout to tune.
func runReader(t *testing.T, r *TxnStateReader, batches ...tail.Batch) []Observation {
	t.Helper()
	in := make(chan tail.Batch, len(batches))
	for _, b := range batches {
		in <- b
	}
	close(in)

	// Buffered past any plausible emission count so Run never blocks on the send.
	out := make(chan Observation, 64)
	require.NoError(t, r.Run(context.Background(), in, out))
	close(out)

	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	return got
}

func TestTxnStateReader_FootprintBearingRecordProducesAnObservationCarryingItsTopics(t *testing.T) {
	// An Ongoing record is the coordinator's own statement of which topics the
	// transaction has enrolled. That footprint is the entire point of reading this
	// log, so it must reach the accumulator with its topics intact.
	r := NewTxnStateReader(DefaultTxnStateTopic, NewTxnCatalog())

	got := runReader(t, r, stateBatch(tail.Record{
		Offset: 7,
		Key:    txnKey("payments-app-0"),
		Value:  txnValue(42, 1, "payments.approved", "payments.ledger"), // 1 = Ongoing
	}))

	require.Len(t, got, 1)
	assert.Equal(t, "payments-app-0", got[0].TxnID)
	assert.Equal(t, int64(42), got[0].ProducerID)
	assert.Equal(t, []string{"payments.approved", "payments.ledger"}, got[0].Topics)
	assert.Equal(t, SourceTxnStateLog, got[0].Source)
	assert.False(t, got[0].ObservedAt.IsZero())
	assert.Equal(t, int64(1), r.Stats().Footprints)
	assert.Equal(t, int64(1), r.Stats().RecordsSeen)
}

func TestTxnStateReader_CompletedTransactionRegistersInTheCatalogButProducesNoObservation(t *testing.T) {
	// Once a transaction completes, the coordinator rewrites its state record with the
	// partition set CLEARED. Emitting an observation from it would report an empty
	// footprint as if it were the truth and, worse, credit the transaction with having
	// touched nothing. The transactional id and producer id are still on the record,
	// though, and the enrichment phases correlate on exactly those — so the catalog
	// registration must happen regardless of status.
	cat := NewTxnCatalog()
	r := NewTxnStateReader(DefaultTxnStateTopic, cat)

	got := runReader(t, r, stateBatch(tail.Record{
		Offset: 11,
		Key:    txnKey("payments-app-0"),
		Value:  txnValue(42, 4), // 4 = CompleteCommit, no partitions
	}))

	assert.Empty(t, got)
	assert.Equal(t, []string{"payments-app-0"}, cat.TxnIDs())
	assert.Equal(t, map[int64]string{42: "payments-app-0"}, cat.ProducerIDToTxnID())

	st := r.Stats()
	assert.Equal(t, int64(1), st.RecordsSeen)
	assert.Equal(t, int64(0), st.Footprints)
	assert.Equal(t, int64(1), st.Committed)
	assert.Equal(t, int64(1), st.Empty)
}
