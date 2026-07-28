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

// TxnStateStats is a snapshot of what the transaction-state reader decoded. It is
// what the run summary reports for this phase.
//
// Reader lag is deliberately absent: the tail component owns offset tracking and
// reports lag against the last stable offset, so duplicating a records-behind figure
// here would give the summary two disagreeing answers to the same question.
type TxnStateStats struct {
	// RecordsSeen counts every record this reader was handed, including tombstones
	// and records it could not decode.
	RecordsSeen int64
	// Footprints counts records that carried a topic-partition set and so produced an
	// Observation.
	Footprints int64
	// Empty counts records whose status carries no footprint because the coordinator
	// had already cleared the partition set.
	Empty int64
	// Committed and Aborted count the terminal CompleteCommit / CompleteAbort records
	// seen. Reading from earliest, this is the completions the compacted log still
	// retains plus those occurring inside the window — not a lifetime total, since
	// compaction may already have collapsed an old transaction down to one record.
	Committed int64
	Aborted   int64
	// Tombstones counts null-valued records, which is compaction retiring a
	// transactional id rather than anything malformed.
	Tombstones int64

	// KeyDecodeErrors and ValueDecodeErrors count broker-supplied records this reader
	// could not parse. They are the format-drift alarm — a non-zero count means the
	// run may have missed footprints — and are kept apart so the summary can name
	// which half of the record drifted.
	KeyDecodeErrors   int64
	ValueDecodeErrors int64
}

// TxnStateReader decodes __transaction_state records — the transaction coordinator's
// own log — into Observations, reconstructing each transaction's topic footprint
// straight from the records the coordinator wrote.
//
// It does not own its Kafka connection. It consumes tail.Batch values from a channel
// so that ONE tail instance can serve both this reader and the __consumer_offsets
// one; batches are demultiplexed on topic. The caller starts that tail at the
// EARLIEST offset, so the run also recovers footprints the log retained from before
// it started, and keeps it in a continuous fetch loop rather than a periodic poll —
// a short transaction's record can be compacted away between polls.
//
// As it decodes it registers every transactional id and producer id in the shared
// TxnCatalog, which the enrichment phases read instead of calling the transaction
// admin APIs.
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

// NewTxnStateReader builds a reader over the named transaction-state topic, which is
// DefaultTxnStateTopic unless the operator overrode it. Only batches carrying that
// topic are decoded. Every decoded record is registered in catalog.
func NewTxnStateReader(topic string, catalog *TxnCatalog) *TxnStateReader {
	return &TxnStateReader{topic: topic, catalog: catalog}
}

// Stats returns a snapshot of the reader's counters. Safe to call while Run is in
// flight, which is what the periodic keep-up signal does.
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

// Run consumes batches from in until the channel closes or ctx ends, emitting an
// Observation onto out for every footprint-bearing record.
//
// This is the contract the command wiring implements: it owns the tail.Tail, reads
// its single batch channel, and hands each batch to the reader whose topic it names.
// Run takes a receive-only channel rather than a Tail precisely so one tail can be
// demultiplexed across this reader and the __consumer_offsets one.
//
// out must be drained for the duration of the run. Run returns nil on both ordinary
// endings — input exhausted, or context cancelled — because neither is a failure of
// the run; the counters on Stats are what report whether the read was clean.
func (r *TxnStateReader) Run(ctx context.Context, in <-chan tail.Batch, out chan<- Observation) error {
	for b := range in {
		// Demultiplex. One Tail serves this reader and the __consumer_offsets one over a
		// single channel, so a batch that is not ours is skipped rather than decoded:
		// feeding an offset-commit record to the TransactionLogValue decoder would fire
		// the format-drift alarm on a cluster whose format never drifted.
		if b.Topic != r.topic {
			continue
		}
		// A control batch is the transaction marker itself, not application data. The
		// tail component delivers control batches flagged rather than filtering them, so
		// dropping them is this consumer's job, exactly as it is ConsumerOffsetsTail's.
		// A marker's key and value are a completely different schema — four and six
		// bytes — so feeding them to the TransactionLog decoders ticks the decode-error
		// counters, and those counters are the format-drift alarm: firing them on a
		// cluster whose format never drifted discredits the one signal that tells an
		// operator a broker upgrade has moved the schema out from under the run.
		if b.Control {
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
			// Deliberately NOT key.TransactionalID, which is in scope here and which the
			// ported source logged: kcp.log is Debug+ unconditionally and is attached to
			// support tickets, so the offset is the diagnostic and the id is disclosure.
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
	// A consumer that has stopped draining must not pin the reader open, or shutdown
	// deadlocks behind an unread send and the command never writes its artifacts.
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
