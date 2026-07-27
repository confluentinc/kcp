package discovery

import (
	"context"
	"sync"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/txnlog"
)

// DefaultConsumerOffsetsTopic is Kafka's internal consumer-offsets log.
const DefaultConsumerOffsetsTopic = "__consumer_offsets"

// finalFlushTimeout bounds the fresh context the final flush runs on.
const finalFlushTimeout = 10 * time.Second

// ConsumerOffsetsTail recovers the CONSUMED (input) topics of exactly-once
// applications by joining transactional offset commits to transactions on the
// record-batch producer id.
//
// A transaction's footprint names only what it PRODUCED. When an exactly-once
// app commits its consumer offsets inside the transaction
// (sendOffsetsToTransaction), that commit is written to __consumer_offsets as a
// TRANSACTIONAL record: the batch header carries the producer id, and the
// record key carries the consumer group and the consumed topic. The
// __transaction_state reader has already recorded which transactional id that
// producer id belongs to, so joining the two on the producer id ties the
// consumed topic to the exact transaction — with no assumption about how the
// group id and the transactional id are named.
//
// That join is the entire reason this feature reads raw fetch responses rather
// than using sarama's consumer API, which discards the batch header.
type ConsumerOffsetsTail struct {
	catalog *TxnCatalog
	topic   string

	mu sync.Mutex
	// pending holds sightings not yet resolved to a transaction: producer id ->
	// the set of consumed topics it committed offsets for. An entry is removed
	// once its producer id resolves and is emitted.
	pending map[int64]map[string]struct{}
}

// ConsumerOffsetsOptions configures a ConsumerOffsetsTail. The zero value is
// usable: every field falls back to its package default.
type ConsumerOffsetsOptions struct {
	// Topic is the offsets log to demultiplex out of the shared tail channel.
	// It must match the TopicSpec the tail was started with, so it is set from
	// the same place rather than hardcoded on both sides. Defaults to
	// DefaultConsumerOffsetsTopic.
	Topic string
}

// NewConsumerOffsetsTail builds a tail consumer over the shared catalog the
// __transaction_state reader populates.
func NewConsumerOffsetsTail(catalog *TxnCatalog, opts ConsumerOffsetsOptions) *ConsumerOffsetsTail {
	if opts.Topic == "" {
		opts.Topic = DefaultConsumerOffsetsTopic
	}
	return &ConsumerOffsetsTail{
		catalog: catalog,
		topic:   opts.Topic,
		pending: make(map[int64]map[string]struct{}),
	}
}

// HandleBatch records the transactional offset commits carried by one batch.
//
// It is the demultiplexing entry point: one tail instance serves both this
// consumer and the __transaction_state reader, fanning every partition of both
// topics into a single channel, so batches for other topics are this
// consumer's to ignore.
func (t *ConsumerOffsetsTail) HandleBatch(b tail.Batch) {
	if b.Topic != t.topic {
		return
	}
	// Only a commit written inside a transaction correlates. A batch header
	// carries a producer id for a plain idempotent producer too, so joining a
	// non-transactional commit on it would attribute an unrelated consumer's
	// topics to whichever transaction happened to share that id.
	if !b.IsTransactional {
		return
	}
	// A control batch is the transaction marker itself, not application data.
	// The tail component delivers control batches flagged rather than filtering
	// them, so dropping them is this consumer's job — and a marker is
	// transactional with a real producer id, so no other filter here stops it.
	if b.Control {
		return
	}
	// -1 is Kafka's "no producer id" sentinel and 0 is an absent field. Keying a
	// pending entry on either would pool every producer without an id into one
	// bucket, and the moment any transaction was catalogued under that sentinel
	// the whole pool would be attributed to it. The catalog refuses to WRITE a
	// non-positive id; refusing to READ one is this side's job.
	if b.ProducerID <= 0 {
		return
	}
	for _, r := range b.Records {
		key, err := txnlog.DecodeOffsetKey(r.Key)
		if err != nil {
			continue
		}
		t.recordCommit(b.ProducerID, key.Topic)
	}
}

// recordCommit buffers one sighting: producer id pid committed an offset for
// consumed topic. It stays buffered until the producer id resolves to a
// transaction.
func (t *ConsumerOffsetsTail) recordCommit(pid int64, topic string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	set := t.pending[pid]
	if set == nil {
		set = make(map[string]struct{})
		t.pending[pid] = set
	}
	set[topic] = struct{}{}
}

// FinalFlush resolves everything still buffered, on a fresh context.
//
// It exists because most resolutions land here: a short-lived transaction may
// only reach the __transaction_state reader late in the window, long after its
// offset commit was buffered. The observation context is already cancelled by
// the time the run winds down, so a flush on it would send nothing.
func (t *ConsumerOffsetsTail) FinalFlush(out chan<- Observation) {
	ctx, cancel := context.WithTimeout(context.Background(), finalFlushTimeout)
	defer cancel()
	t.resolveAndFlush(ctx, out)
}

// resolveAndFlush reads the current producer-id -> transactional-id map from
// the shared catalog and emits an observation for every pending sighting that
// now resolves.
func (t *ConsumerOffsetsTail) resolveAndFlush(ctx context.Context, out chan<- Observation) {
	for _, obs := range t.resolveWith(t.catalog.ProducerIDToTxnID(), time.Now()) {
		select {
		case out <- obs:
		case <-ctx.Done():
			return
		}
	}
}

// resolveWith is the pure join at the heart of this unit: every pending
// sighting whose producer id appears in pidToTxn becomes an observation under
// that transaction and leaves the buffer. It keys purely on the producer id, so
// it correlates a consumer group to its transaction however their names relate.
//
// Observations are returned rather than sent so the caller can respect its
// context without holding the lock.
func (t *ConsumerOffsetsTail) resolveWith(pidToTxn map[int64]string, now time.Time) []Observation {
	t.mu.Lock()
	defer t.mu.Unlock()

	var out []Observation
	for pid, topics := range t.pending {
		txnID, ok := pidToTxn[pid]
		if !ok {
			// Its transaction has not been decoded yet — keep waiting.
			continue
		}
		out = append(out, Observation{
			TxnID:      txnID,
			ProducerID: pid,
			Topics:     sortedKeys(topics),
			// A recovered consumed input is by definition an input to a
			// transaction that committed consumer offsets.
			ReadProcessWrite: true,
			Source:           SourceConsumerOffsets,
			ObservedAt:       now,
		})
		delete(t.pending, pid)
	}
	return out
}
