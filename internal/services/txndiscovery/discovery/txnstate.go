package discovery

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/txnlog"
)

// DefaultTxnStateTopic is Kafka's internal transaction-coordinator log.
const DefaultTxnStateTopic = "__transaction_state"

// TxnStateStats is a snapshot of what the transaction-state reader decoded.
type TxnStateStats struct {
	RecordsSeen int64
	Footprints  int64
	Empty       int64
	Committed   int64
	Aborted     int64
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
	}
}

// Run drains the batch channel, emitting observations onto out.
func (r *TxnStateReader) Run(ctx context.Context, in <-chan tail.Batch, out chan<- Observation) error {
	for b := range in {
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

	key, err := txnlog.DecodeKey(rec.Key)
	if err != nil {
		return true
	}
	val, err := txnlog.DecodeValue(rec.Value)
	if err != nil {
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
	obs := Observation{
		TxnID:      key.TransactionalID,
		ProducerID: val.ProducerID,
		Topics:     val.Topics(),
		Source:     SourceTxnStateLog,
		ObservedAt: time.Now(),
	}
	select {
	case out <- obs:
		return true
	case <-ctx.Done():
		return false
	}
}
