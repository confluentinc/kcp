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
}

// TxnStateReader decodes __transaction_state records into Observations.
type TxnStateReader struct {
	topic   string
	catalog *TxnCatalog

	recordsSeen atomic.Int64
	footprints  atomic.Int64
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
