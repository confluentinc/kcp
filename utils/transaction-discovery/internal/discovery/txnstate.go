package discovery

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/txnlog"
)

// DefaultTxnStateTopic is Kafka's internal transaction-coordinator log.
const DefaultTxnStateTopic = "__transaction_state"

// TxnStateLogTail reads the __transaction_state internal topic — the transaction
// coordinator's own log — and reconstructs each transaction's footprint straight
// from its state records. It is the single source of truth for the discovery run:
// every footprint-bearing (Ongoing / PrepareCommit) record carries the set of
// topic-partitions the transaction enrolled, keyed by transactional.id.
//
// It reads from the EARLIEST offset, not the tail, so it also recovers the footprints
// the log still retains from before the run started, not just records written while
// it is connected. And it must sit in a continuous fetch loop — not a periodic poll —
// or a short transaction's record could be compacted away before we read it.
//
// As it decodes, it registers every transactional.id and its producer id in the shared
// TxnCatalog, which the consumer-group phases read instead of calling the transaction
// admin APIs.
//
// It only works where the internal topic is readable: self-managed / Confluent
// Platform and MSK provisioned clusters. On Confluent Cloud and MSK Serverless the
// topic is inaccessible; the caller gates the run on TxnStateAvailable and fails fast
// there, since there is no longer an admin-sampling fallback.
type TxnStateLogTail struct {
	Consumer *kgo.Client // configured to consume Topic
	Topic    string
	Catalog  *TxnCatalog // populated with (transactional.id, producer id) per record
	Log      *slog.Logger

	recordsSeen       atomic.Int64
	footprints        atomic.Int64
	tombstones        atomic.Int64
	empty             atomic.Int64
	committed         atomic.Int64
	aborted           atomic.Int64
	keyDecodeErrors   atomic.Int64
	valueDecodeErrors atomic.Int64
	maxLag            atomic.Int64
	finalLag          atomic.Int64
}

// TailStats is a snapshot of how the __transaction_state tail kept up over the run.
// MaxLag / FinalLag are in RECORDS behind the partition high-watermark, sampled per
// fetch: if the tail cannot decode as fast as the coordinator writes state records,
// lag grows and short transactions risk being compacted away before we read them —
// so a large, non-decreasing lag is the signal that the reader is not keeping up.
// ValueDecodeErrors also counts unsupported/unknown record versions (format drift).
type TailStats struct {
	RecordsSeen int64
	Footprints  int64
	Tombstones  int64
	Empty       int64
	// Committed / Aborted count the terminal CompleteCommit / CompleteAbort records
	// seen during the run. Reading from the earliest offset, this is a count of the
	// completions the compacted log still retains plus those that occur within the
	// window — not a guaranteed lifetime total, since compaction may already have
	// collapsed an old transaction's records down to a single empty-footprint record.
	Committed         int64
	Aborted           int64
	KeyDecodeErrors   int64
	ValueDecodeErrors int64
	MaxLag            int64
	FinalLag          int64
}

func (t *TxnStateLogTail) Name() string { return SourceTxnStateLog }

// Stats returns a snapshot of the tail metrics collected so far.
func (t *TxnStateLogTail) Stats() TailStats {
	return TailStats{
		RecordsSeen:       t.recordsSeen.Load(),
		Footprints:        t.footprints.Load(),
		Tombstones:        t.tombstones.Load(),
		Empty:             t.empty.Load(),
		Committed:         t.committed.Load(),
		Aborted:           t.aborted.Load(),
		KeyDecodeErrors:   t.keyDecodeErrors.Load(),
		ValueDecodeErrors: t.valueDecodeErrors.Load(),
		MaxLag:            t.maxLag.Load(),
		FinalLag:          t.finalLag.Load(),
	}
}

func (t *TxnStateLogTail) Run(ctx context.Context, out chan<- Observation) error {
	t.Log.Info("tailing transaction-state log", "topic", t.Topic)
	for {
		if ctx.Err() != nil {
			return nil
		}
		fs := t.Consumer.PollFetches(ctx)
		if fs.IsClientClosed() {
			return nil
		}
		fs.EachError(func(topic string, p int32, err error) {
			if ctx.Err() == nil {
				t.Log.Warn("transaction-state fetch error", "topic", topic, "partition", p, "err", err)
			}
		})
		t.recordLag(&fs)
		iter := fs.RecordIter()
		for !iter.Done() {
			if !t.handle(ctx, iter.Next(), out) {
				return nil
			}
		}
	}
}

// recordLag samples how far behind the tail is, in records, by comparing each
// partition's high-watermark to the offset of the last record we just fetched. It is
// only a lower bound (a partition idle in this fetch contributes nothing), but under
// load it is the direct "are we keeping up" signal.
func (t *TxnStateLogTail) recordLag(fs *kgo.Fetches) {
	var total int64
	sawRecords := false
	fs.EachPartition(func(p kgo.FetchTopicPartition) {
		n := len(p.Records)
		if n == 0 || p.HighWatermark <= 0 {
			return
		}
		sawRecords = true
		if lag := p.HighWatermark - (p.Records[n-1].Offset + 1); lag > 0 {
			total += lag
		}
	})
	if !sawRecords {
		return
	}
	t.finalLag.Store(total)
	for {
		cur := t.maxLag.Load()
		if total <= cur || t.maxLag.CompareAndSwap(cur, total) {
			break
		}
	}
}

// handle decodes one record and emits an Observation. It returns false only if ctx
// is cancelled while sending, signalling the caller to stop.
func (t *TxnStateLogTail) handle(ctx context.Context, r *kgo.Record, out chan<- Observation) bool {
	t.recordsSeen.Add(1)
	if len(r.Value) == 0 {
		t.tombstones.Add(1)
		return true // tombstone from compaction — no footprint
	}
	key, err := txnlog.DecodeKey(r.Key)
	if err != nil {
		t.keyDecodeErrors.Add(1)
		t.Log.Warn("decode transaction-state key failed", "err", err)
		return true
	}
	val, err := txnlog.DecodeValue(r.Value)
	if err != nil {
		// Exactly the drift signal Emma flagged: if the internal record format
		// changed, decoding fails here and we surface it rather than mis-parse.
		t.valueDecodeErrors.Add(1)
		t.Log.Warn("decode transaction-state value failed", "txn", key.TransactionalID, "err", err)
		return true
	}
	// Register the transaction with the shared catalog before anything else: even
	// Empty / Complete* records (which carry no footprint) still carry the
	// transactional.id and producer id the consumer-group phases correlate on.
	t.Catalog.Observe(key.TransactionalID, val.ProducerID)
	// Count terminal states so the run can report how many transactions committed vs
	// aborted in the window. These carry no footprint (the coordinator has cleared the
	// partition set), so they fall through to the Empty branch below for record-keeping.
	switch val.Status {
	case txnlog.StatusCompleteCommit:
		t.committed.Add(1)
	case txnlog.StatusCompleteAbort:
		t.aborted.Add(1)
	}
	if !val.Status.HasFootprint() {
		t.empty.Add(1)
		return true // Empty / Complete* / Dead: the partition set is already cleared
	}
	topics := val.Topics()
	if len(topics) == 0 {
		t.empty.Add(1)
		return true
	}
	t.footprints.Add(1)
	obs := Observation{
		TxnID:            key.TransactionalID,
		ProducerID:       val.ProducerID,
		Topics:           topics,
		ReadProcessWrite: containsTopic(topics, DefaultConsumerOffsetsTopic),
		Source:           t.Name(),
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

// TxnStateAvailable reports whether the __transaction_state topic can be read on this
// cluster. It returns false (with the reason, if any) on managed clusters that hide
// the topic or deny access. Because __transaction_state is now the source of truth
// with no admin-sampling fallback, the caller fails fast when this returns false.
func TxnStateAvailable(ctx context.Context, admin *kadm.Client, topic string) (bool, error) {
	return internalTopicReadable(ctx, admin, topic)
}

// internalTopicReadable reports whether an internal topic can be described (and so,
// in practice, read) on this cluster. It is the shared gate for the two internal-topic
// readers (the __transaction_state source of truth and the Phase 4 __consumer_offsets
// tail): on managed clusters that hide the topic or deny access it returns false with
// the reason. Note this checks describe access; a read-only ACL gap would still surface
// as a fetch error once reading begins.
func internalTopicReadable(ctx context.Context, admin *kadm.Client, topic string) (bool, error) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := admin.ListTopics(c, topic)
	if err != nil {
		return false, err
	}
	d, ok := resp[topic]
	if !ok {
		return false, nil
	}
	if d.Err != nil {
		return false, d.Err
	}
	return true, nil
}
