package discovery

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/txnlog"
)

// DefaultTxnStateTopic is Kafka's internal transaction-coordinator log.
const DefaultTxnStateTopic = "__transaction_state"

// DefaultConsumerOffsetsTopic is Kafka's internal consumer-offsets log. A footprint
// enrolling it is how a transaction declares itself read-process-write.
const DefaultConsumerOffsetsTopic = "__consumer_offsets"

// TxnStateStats is a snapshot of what the transaction-state reader decoded.
type TxnStateStats struct {
	RecordsSeen int64
	Footprints  int64
	Empty       int64
	Committed   int64
	Aborted     int64
	Tombstones  int64

	KeyDecodeErrors   int64
	ValueDecodeErrors int64
}

// TxnStateReader decodes __transaction_state records into Observations.
type TxnStateReader struct {
	topic   string
	catalog *TxnCatalog

	recordsSeen atomic.Int64
	footprints  atomic.Int64
	empty       atomic.Int64
	committed   atomic.Int64
	aborted     atomic.Int64
	tombstones  atomic.Int64

	keyDecodeErrors   atomic.Int64
	valueDecodeErrors atomic.Int64
}

// NewTxnStateReader builds a reader over the named transaction-state topic.
func NewTxnStateReader(topic string, catalog *TxnCatalog) *TxnStateReader {
	return &TxnStateReader{topic: topic, catalog: catalog}
}

// Stats returns a snapshot of the reader's counters.
func (r *TxnStateReader) Stats() TxnStateStats {
	return TxnStateStats{
		RecordsSeen: r.recordsSeen.Load(),
		Footprints:  r.footprints.Load(),
		Empty:       r.empty.Load(),
		Committed:   r.committed.Load(),
		Aborted:     r.aborted.Load(),
		Tombstones:  r.tombstones.Load(),

		KeyDecodeErrors:   r.keyDecodeErrors.Load(),
		ValueDecodeErrors: r.valueDecodeErrors.Load(),
	}
}

// Run drains the batch channel, emitting observations onto out.
func (r *TxnStateReader) Run(ctx context.Context, in <-chan tail.Batch, out chan<- Observation) error {
	for b := range in {
		// Demultiplex. One Tail serves this reader and the __consumer_offsets one over a
		// single channel, so a batch that is not ours is skipped rather than decoded:
		// feeding an offset-commit record to the TransactionLogValue decoder would fire
		// the format-drift alarm on a cluster whose format never drifted.
		if b.Topic != r.topic {
			continue
		}
		for _, rec := range b.Records {
			if !r.handle(ctx, rec, out) {
				return nil
			}
		}
	}
	return nil
}

// handle decodes one record and emits an Observation for it. It reports false only
// when ctx ended while sending, signalling the caller to stop.
func (r *TxnStateReader) handle(ctx context.Context, rec tail.Record, out chan<- Observation) bool {
	r.recordsSeen.Add(1)

	// A null value is compaction retiring a transactional id, not corruption. It is
	// checked before the decoder so a routine tombstone cannot register as a value
	// decode error and fire the format-drift alarm.
	if len(rec.Value) == 0 {
		r.tombstones.Add(1)
		return true
	}

	key, err := txnlog.DecodeKey(rec.Key)
	if err != nil {
		// Counted, not fatal. One unreadable record must not end the observation
		// window — the run would then report what it had read so far as the whole
		// truth. The count is what the summary reports; the per-record detail stays at
		// Debug so sustained drift cannot flood the console under --verbose.
		r.keyDecodeErrors.Add(1)
		slog.Debug("⏭️ skipped a transaction-state record whose key would not decode",
			"offset", rec.Offset, "error", err)
		return true
	}
	val, err := txnlog.DecodeValue(rec.Value)
	if err != nil {
		// The format-drift alarm. If a broker upgrade changes the TransactionLogValue
		// schema the reader stops understanding footprints, and without this counter the
		// run would finish "successfully" having found nothing — reporting an
		// exactly-once cluster as having no transactional coupling at all. Counted
		// separately from the key errors so the summary can name which half drifted.
		r.valueDecodeErrors.Add(1)
		slog.Debug("⏭️ skipped a transaction-state record whose value would not decode",
			"offset", rec.Offset, "error", err)
		return true
	}

	// Register with the shared catalog before anything else. Even a Complete* record,
	// which carries no footprint, still carries the transactional id and producer id
	// the enrichment phases correlate on — so registration is unconditional on status.
	r.catalog.Observe(key.TransactionalID, val.ProducerID)

	// Count the terminal states so the run can report how many transactions committed
	// versus aborted in the window. Reading from earliest, this counts the completions
	// the compacted log still retains plus those occurring inside the window, not a
	// guaranteed lifetime total.
	switch val.Status {
	case txnlog.StatusCompleteCommit:
		r.committed.Add(1)
	case txnlog.StatusCompleteAbort:
		r.aborted.Add(1)
	}

	if !val.Status.HasFootprint() {
		// Empty / Complete* / Dead: the coordinator has already cleared the partition
		// set, so there is no footprint to report — only the catalog entry above.
		r.empty.Add(1)
		return true
	}

	r.footprints.Add(1)
	topics := val.Topics()
	obs := Observation{
		TxnID:      key.TransactionalID,
		ProducerID: val.ProducerID,
		// The RAW footprint, internal topics included. Filtering belongs to the grouping
		// stage; dropping __consumer_offsets here would erase the evidence for the flag
		// below from the audit trail.
		Topics:           topics,
		ReadProcessWrite: containsTopic(topics, DefaultConsumerOffsetsTopic),
		Source:           SourceTxnStateLog,
		ObservedAt:       time.Now(),
	}
	select {
	case out <- obs:
		return true
	case <-ctx.Done():
		return false
	}
}

// containsTopic reports whether want is in topics.
func containsTopic(topics []string, want string) bool {
	for _, t := range topics {
		if t == want {
			return true
		}
	}
	return false
}
