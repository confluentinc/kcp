package discovery

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"testing"
	"time"

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

func TestTxnStateReader_TombstoneIsCountedAndProducesNoObservation(t *testing.T) {
	// Compaction writes a null value to retire a transactional id. There is nothing to
	// decode, so it must be counted as a tombstone and skipped — NOT routed into the
	// value decoder, which would report a truncated record and fire the format-drift
	// alarm on a completely routine event.
	r := NewTxnStateReader(DefaultTxnStateTopic, NewTxnCatalog())

	got := runReader(t, r, stateBatch(tail.Record{
		Offset: 19,
		Key:    txnKey("payments-app-0"),
		Value:  nil,
	}))

	assert.Empty(t, got)

	st := r.Stats()
	assert.Equal(t, int64(1), st.RecordsSeen)
	assert.Equal(t, int64(1), st.Tombstones)
	assert.Equal(t, int64(0), st.ValueDecodeErrors)
	assert.Equal(t, int64(0), st.Footprints)
}

func TestTxnStateReader_FootprintContainingConsumerOffsetsIsReadProcessWrite(t *testing.T) {
	// A transaction that enrolled __consumer_offsets committed consumer offsets inside
	// itself, which makes it a consume-transform-produce app. Its CONSUMED input topics
	// are not in this footprint — only its outputs are — so the flag is what tells the
	// downstream enrichment phases there are inputs still to recover.
	r := NewTxnStateReader(DefaultTxnStateTopic, NewTxnCatalog())

	got := runReader(t, r,
		stateBatch(tail.Record{
			Key:   txnKey("streams-app-0"),
			Value: txnValue(7, 1, "orders.enriched", DefaultConsumerOffsetsTopic),
		}),
		stateBatch(tail.Record{
			Key:   txnKey("produce-only-0"),
			Value: txnValue(8, 1, "audit.events"),
		}),
	)

	require.Len(t, got, 2)
	assert.True(t, got[0].ReadProcessWrite, "a footprint enrolling __consumer_offsets is read-process-write")
	// The raw footprint is passed through INCLUDING the internal topic: filtering is the
	// grouping stage's job, and dropping it here would make the observation unauditable.
	assert.Equal(t, []string{"orders.enriched", DefaultConsumerOffsetsTopic}, got[0].Topics)
	assert.False(t, got[1].ReadProcessWrite, "a produce-only footprint is not read-process-write")
}

func TestTxnStateReader_EveryStatusRegistersItsTxnIDAndProducerIDInTheCatalog(t *testing.T) {
	// The catalog is what the enrichment phases correlate on instead of calling the
	// transaction admin APIs, and both facts they need — the transactional id and the
	// producer id — are on EVERY state record whatever its status. Registering only
	// footprint-bearing records would hide every transaction that had already finished
	// when the run started, which on a compacted log is most of them.
	cat := NewTxnCatalog()
	r := NewTxnStateReader(DefaultTxnStateTopic, cat)

	// One record per status, none of which is Ongoing/PrepareCommit/PrepareAbort except
	// where noted, and all with their partition set cleared as the coordinator leaves it.
	var records []tail.Record
	for i, status := range []int8{0, 4, 5, 6, 7} { // Empty, CompleteCommit, CompleteAbort, Dead, PrepareEpochFence
		records = append(records, tail.Record{
			Key:   txnKey(fmt.Sprintf("app-%d", i)),
			Value: txnValue(int64(100+i), status),
		})
	}

	got := runReader(t, r, stateBatch(records...))

	assert.Empty(t, got, "no status in this set carries a footprint")

	ids := cat.TxnIDs()
	sort.Strings(ids)
	assert.Equal(t, []string{"app-0", "app-1", "app-2", "app-3", "app-4"}, ids)
	assert.Equal(t, map[int64]string{
		100: "app-0", 101: "app-1", 102: "app-2", 103: "app-3", 104: "app-4",
	}, cat.ProducerIDToTxnID())

	st := r.Stats()
	assert.Equal(t, int64(5), st.RecordsSeen)
	assert.Equal(t, int64(5), st.Empty)
	assert.Equal(t, int64(1), st.Committed)
	assert.Equal(t, int64(1), st.Aborted)
}

func TestTxnStateReader_UndecodableKeyIsCountedAndDoesNotStopTheReader(t *testing.T) {
	// The broker is untrusted input here: these are internal record schemas outside the
	// stable client protocol. One unreadable key must not take the reader down with it,
	// or a single malformed record ends the observation window and the run silently
	// reports whatever it had managed to read so far as the whole truth.
	r := NewTxnStateReader(DefaultTxnStateTopic, NewTxnCatalog())

	got := runReader(t, r, stateBatch(
		// Key version 9 — not a version that exists, so the decoder rejects it rather
		// than guessing at the layout.
		tail.Record{Key: concat(be16(9), kstr("evil-app")), Value: txnValue(1, 1, "a")},
		// Truncated: the length prefix claims more bytes than the buffer holds.
		tail.Record{Key: concat(be16(0), be16(64), []byte("short")), Value: txnValue(2, 1, "b")},
		// A good record after the bad ones proves the loop kept going.
		tail.Record{Key: txnKey("healthy-app"), Value: txnValue(3, 1, "c")},
	))

	require.Len(t, got, 1, "the reader must survive the bad keys and still emit the good record")
	assert.Equal(t, "healthy-app", got[0].TxnID)

	st := r.Stats()
	assert.Equal(t, int64(3), st.RecordsSeen)
	assert.Equal(t, int64(2), st.KeyDecodeErrors)
	// A bad key must not be miscounted as the format-drift alarm for values.
	assert.Equal(t, int64(0), st.ValueDecodeErrors)
	assert.Equal(t, int64(1), st.Footprints)
}

func TestTxnStateReader_UndecodableValueIncrementsItsOwnCounterAndDoesNotStopTheReader(t *testing.T) {
	// Value decode failures are the format-drift alarm. If a broker upgrade changes the
	// TransactionLogValue schema the reader stops understanding footprints, and the run
	// would otherwise finish "successfully" having found nothing — reporting an EOS
	// cluster as having no transactional coupling at all. The counter is tracked apart
	// from the key counter so the summary can name which half of the record drifted.
	r := NewTxnStateReader(DefaultTxnStateTopic, NewTxnCatalog())

	got := runReader(t, r, stateBatch(
		// Value version 99 — the decoder rejects an unknown schema rather than guessing.
		tail.Record{Key: txnKey("drift-app"), Value: concat(be16(99), be64(1))},
		// Truncated mid-record: the header is present but the body is cut short.
		tail.Record{Key: txnKey("cut-app"), Value: concat(be16(0), be64(2), be16(1))},
		tail.Record{Key: txnKey("healthy-app"), Value: txnValue(3, 1, "c")},
	))

	require.Len(t, got, 1, "the reader must survive the bad values and still emit the good record")
	assert.Equal(t, "healthy-app", got[0].TxnID)

	st := r.Stats()
	assert.Equal(t, int64(3), st.RecordsSeen)
	assert.Equal(t, int64(2), st.ValueDecodeErrors)
	// The keys decoded fine, so this must stay clean or the summary blames the wrong half.
	assert.Equal(t, int64(0), st.KeyDecodeErrors)
	// A record whose value would not decode is NOT a tombstone: conflating the two would
	// silence the alarm behind a routine compaction count.
	assert.Equal(t, int64(0), st.Tombstones)
	assert.Equal(t, int64(1), st.Footprints)
}

func TestTxnStateReader_IgnoresBatchesFromAnotherTopic(t *testing.T) {
	// One Tail instance serves both this reader and the __consumer_offsets one, and a
	// single channel carries both topics, so every consumer must demultiplex on the
	// batch's topic. Without this filter a __consumer_offsets commit record would be
	// fed to the TransactionLogValue decoder, which cannot read it — turning a
	// correctly wired run into a flood of value decode errors and firing the
	// format-drift alarm on a cluster whose format never drifted.
	r := NewTxnStateReader(DefaultTxnStateTopic, NewTxnCatalog())

	got := runReader(t, r,
		tail.Batch{
			Topic:   DefaultConsumerOffsetsTopic,
			Records: []tail.Record{{Key: []byte("an offset commit key"), Value: []byte("not a txn state value")}},
		},
		stateBatch(tail.Record{Key: txnKey("mine"), Value: txnValue(1, 1, "a")}),
	)

	require.Len(t, got, 1)
	assert.Equal(t, "mine", got[0].TxnID)

	st := r.Stats()
	assert.Equal(t, int64(1), st.RecordsSeen, "a foreign topic's records are not this reader's to count")
	assert.Equal(t, int64(0), st.KeyDecodeErrors)
	assert.Equal(t, int64(0), st.ValueDecodeErrors, "a foreign record must not fire the format-drift alarm")
}

func TestTxnStateReader_CancelledContextStopsTheReaderRatherThanBlockingOnAnUnreadSend(t *testing.T) {
	// At shutdown the orchestrator stops draining observations before every source has
	// finished. A reader that blocked on the send would pin the whole run open and the
	// command would hang instead of writing its artifacts. The send must lose to
	// cancellation.
	r := NewTxnStateReader(DefaultTxnStateTopic, NewTxnCatalog())

	in := make(chan tail.Batch, 1)
	in <- stateBatch(tail.Record{Key: txnKey("app"), Value: txnValue(1, 1, "a")})
	// Deliberately NOT closed: Run must return because the context ended, not because
	// its input ran out.

	// Unbuffered and never read, so the send can only complete via cancellation.
	out := make(chan Observation)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, in, out) }()

	select {
	case err := <-done:
		assert.NoError(t, err, "cancellation is an ordinary shutdown, not a run failure")
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked on an unread send after its context was cancelled")
	}
}
