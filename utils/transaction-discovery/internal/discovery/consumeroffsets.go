package discovery

import (
	"context"
	"encoding/binary"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// DefaultConsumerOffsetsTopic is Kafka's internal consumer-offsets log.
const DefaultConsumerOffsetsTopic = "__consumer_offsets"

// ConsumerOffsetsTail is the Phase 4 data source. Like Phase 3 it recovers the
// CONSUMED (input) topics of read-process-write / EOS apps — invisible in a
// transaction footprint — but it does so by EXACT producer-id correlation rather than
// the Kafka Streams naming convention, so it covers arbitrary consumer+producer EOS
// apps whose transactional.id bears no relation to their group.id.
//
// The mechanism, entirely from data Kafka already records:
//   - It tails __consumer_offsets. When an EOS app commits offsets inside its
//     transaction (sendOffsetsToTransaction), that commit is written here as a
//     TRANSACTIONAL record. franz-go exposes the batch header on consumed records, so
//     each such record yields the producer id that wrote it (Record.ProducerID,
//     Record.Attrs.IsTransactional); the record KEY (kmsg.OffsetCommitKey) yields the
//     consumer group and the CONSUMED topic it committed an offset for.
//   - The producerID -> txnID mapping comes from the shared TxnCatalog the
//     __transaction_state reader populates (each state record carries both), so no
//     ListTransactions call is needed.
//   - Joining on producer id ties the consumed topic to the exact transaction, and it
//     is emitted under that transactional id so the existing union-find folds the
//     input into the produced-topic group — no naming assumption anywhere.
//
// Like the __transaction_state reader it reads an internal topic, so it only works
// where that topic is readable (self-managed / Confluent Platform / MSK provisioned;
// not Confluent Cloud / MSK Serverless). Callers gate it with ConsumerOffsetsAvailable
// and fall back to Phase 3's naming heuristic where it is inaccessible. Where both run
// they compose: each emits the same input under the same transactional id and the
// accumulator unions them.
//
// It must sit in a continuous fetch loop — not a periodic poll — or a short EOS app's
// offset-commit record could be compacted away before we read it.
type ConsumerOffsetsTail struct {
	Consumer *kgo.Client   // configured to consume Topic
	Catalog  *TxnCatalog   // producerID -> txnID, populated by the __transaction_state reader
	Topic    string        // usually __consumer_offsets
	Interval time.Duration // cadence for resolving producerID->txnID and flushing
	Log      *slog.Logger

	recordsSeen     atomic.Int64
	txnRecords      atomic.Int64 // transactional offset-commit records (the ones we use)
	keyDecodeErrors atomic.Int64
	maxLag          atomic.Int64
	finalLag        atomic.Int64

	mu sync.Mutex
	// pending holds sightings not yet resolved to a transaction: producerID -> the
	// set of consumed topics it committed offsets for. Entries are removed once their
	// producer id resolves to a listed transaction and is emitted.
	pending map[int64]map[string]struct{}
	groupOf map[int64]string // producerID -> consumer group (provenance / stats)
	// stats, cumulative across the run.
	groupsLinked map[string]struct{}
	correlations map[string]struct{} // "producerID\x00txnID"
	recovered    map[string]struct{} // consumed topics recovered (sorted in Stats)
}

// OffsetsTailStats summarises what Phase 4 recovered and how the tail kept up.
// MaxLag / FinalLag are in RECORDS behind the partition high-watermark (see the
// TxnStateLogTail TailStats doc); a large, non-decreasing lag means the tail is not keeping up.
type OffsetsTailStats struct {
	RecordsSeen     int64
	TxnRecords      int64
	KeyDecodeErrors int64
	MaxLag          int64
	FinalLag        int64
	GroupsLinked    int      // consumer groups exactly correlated to a transaction
	Correlations    int      // (producer id -> transactional id) links resolved
	RecoveredTopics []string // consumed input topics recovered, sorted
}

func (t *ConsumerOffsetsTail) Name() string { return SourceConsumerOffsets }

// Stats returns a snapshot of Phase 4's recovery and keep-up metrics.
func (t *ConsumerOffsetsTail) Stats() OffsetsTailStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return OffsetsTailStats{
		RecordsSeen:     t.recordsSeen.Load(),
		TxnRecords:      t.txnRecords.Load(),
		KeyDecodeErrors: t.keyDecodeErrors.Load(),
		MaxLag:          t.maxLag.Load(),
		FinalLag:        t.finalLag.Load(),
		GroupsLinked:    len(t.groupsLinked),
		Correlations:    len(t.correlations),
		RecoveredTopics: sortedKeys(t.recovered),
	}
}

func (t *ConsumerOffsetsTail) Run(ctx context.Context, out chan<- Observation) error {
	t.Log.Info("tailing consumer-offsets log for EOS offset commits", "topic", t.Topic)

	// The tail loop feeds `pending`; the resolve/flush loop turns resolved sightings
	// into Observations. They are split because tailing must be continuous while the
	// producerID->txnID lookup only needs refreshing on the sampling cadence.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t.tail(ctx)
	}()

	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			// Final flush: most resolutions happen here (a short EOS app's txn may only
			// become listable, or its offset commit only readable, late in the window).
			// ctx is already cancelled, so use a fresh short-lived context.
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			t.resolveAndFlush(fctx, out)
			cancel()
			return nil
		case <-ticker.C:
			t.resolveAndFlush(ctx, out)
		}
	}
}

// tail continuously fetches __consumer_offsets and records transactional offset
// commits into `pending`. It returns when ctx is cancelled or the client is closed.
func (t *ConsumerOffsetsTail) tail(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		fs := t.Consumer.PollFetches(ctx)
		if fs.IsClientClosed() {
			return
		}
		fs.EachError(func(topic string, p int32, err error) {
			if ctx.Err() == nil {
				t.Log.Warn("consumer-offsets fetch error", "topic", topic, "partition", p, "err", err)
			}
		})
		t.recordLag(&fs)
		iter := fs.RecordIter()
		for !iter.Done() {
			t.handle(iter.Next())
		}
	}
}

// handle records one __consumer_offsets record if it is a transactional offset commit.
func (t *ConsumerOffsetsTail) handle(r *kgo.Record) {
	t.recordsSeen.Add(1)

	// Cheapest possible filter first: the overwhelming majority of __consumer_offsets
	// traffic is ordinary (non-transactional) offset commits and group metadata, which
	// carry no producer id to correlate. Only commits written INSIDE a transaction —
	// EOS sendOffsetsToTransaction — matter, and only their batch header ties them to a
	// transaction. Control markers (txn commit/abort) are not delivered by default, but
	// skip them defensively.
	if !r.Attrs.IsTransactional() || r.Attrs.IsControl() {
		return
	}
	if r.ProducerID <= 0 {
		return // no real producer id (should not happen for a transactional record)
	}
	if len(r.Value) == 0 {
		return // tombstone (offset expiry / deletion), not a live commit
	}

	group, topic, ok := decodeOffsetCommitKey(r.Key)
	if !ok {
		t.keyDecodeErrors.Add(1)
		return
	}
	// topic == "" is a group-metadata record (key version 2): no consumed topic.
	// __-prefixed topics are Kafka-internal; grouping drops them anyway.
	if topic == "" || strings.HasPrefix(topic, "__") {
		return
	}

	t.txnRecords.Add(1)
	t.recordCommit(r.ProducerID, group, topic)
}

// recordCommit buffers one transactional offset-commit sighting: producer id `pid`
// (in consumer group `group`) committed an offset for consumed topic `topic`. It is
// held in `pending` until the producer id resolves to a transaction (resolveWith).
func (t *ConsumerOffsetsTail) recordCommit(pid int64, group, topic string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil {
		t.pending = map[int64]map[string]struct{}{}
		t.groupOf = map[int64]string{}
	}
	set := t.pending[pid]
	if set == nil {
		set = map[string]struct{}{}
		t.pending[pid] = set
	}
	set[topic] = struct{}{}
	if group != "" {
		t.groupOf[pid] = group
	}
}

// resolveAndFlush reads the current producerID->txnID map from the shared catalog,
// then emits an Observation for every pending sighting whose producer id now resolves,
// removing it from `pending`. Unresolved sightings stay buffered for a later pass
// (the __transaction_state reader may not have decoded that transaction's record yet).
func (t *ConsumerOffsetsTail) resolveAndFlush(ctx context.Context, out chan<- Observation) {
	pidToTxn := t.Catalog.ProducerIDToTxnID()

	for _, obs := range t.resolveWith(pidToTxn, time.Now()) {
		select {
		case out <- obs:
		case <-ctx.Done():
			return
		}
	}
}

// resolveWith turns every pending sighting whose producer id appears in pidToTxn into
// an Observation under that transaction, removing it from `pending` and folding it into
// the recovery stats. This is the exact join at the heart of Phase 4 — it keys purely
// on producer id, so it correlates a consumer group to its transaction no matter how
// their names relate. It returns the observations for the caller to send (so sending
// can respect ctx without holding the lock).
func (t *ConsumerOffsetsTail) resolveWith(pidToTxn map[int64]string, now time.Time) []Observation {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.recovered == nil {
		t.groupsLinked = map[string]struct{}{}
		t.correlations = map[string]struct{}{}
		t.recovered = map[string]struct{}{}
	}
	var out []Observation
	for pid, topicsSet := range t.pending {
		txnID, ok := pidToTxn[pid]
		if !ok {
			continue // its transaction isn't listable (yet, or already reaped) — keep waiting
		}
		topics := sortedKeys(topicsSet)
		group := t.groupOf[pid]
		out = append(out, Observation{
			TxnID:            txnID,
			ProducerID:       pid,
			Topics:           topics,
			ReadProcessWrite: true,
			Source:           t.Name(),
			ObservedAt:       now,
		})
		if group != "" {
			t.groupsLinked[group] = struct{}{}
		}
		t.correlations[group+"\x00"+txnID] = struct{}{}
		for _, tp := range topics {
			t.recovered[tp] = struct{}{}
		}
		delete(t.pending, pid)
		delete(t.groupOf, pid)
	}
	return out
}

// recordLag mirrors the __transaction_state reader: it samples how far behind the partition
// high-watermark we are, in records, as the "are we keeping up" signal.
func (t *ConsumerOffsetsTail) recordLag(fs *kgo.Fetches) {
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

// decodeOffsetCommitKey parses a __consumer_offsets record key. Key versions 0 and 1
// are OffsetCommitKey {group, topic, partition} — an actual offset commit, which is
// what we want (returns the group and consumed topic). Version 2 is GroupMetadataKey
// {group} — group state, no topic, so topic is returned empty. Anything else is an
// unknown/unsupported key version (ok == false). The kmsg types are the on-disk
// schemas Kafka writes, so no hand-rolled decoder is needed (contrast __transaction_
// state, whose value format is not in kmsg — see internal/txnlog).
func decodeOffsetCommitKey(key []byte) (group, topic string, ok bool) {
	if len(key) < 2 {
		return "", "", false
	}
	switch version := int16(binary.BigEndian.Uint16(key[:2])); version {
	case 0, 1:
		var k kmsg.OffsetCommitKey
		if err := k.ReadFrom(key); err != nil {
			return "", "", false
		}
		return k.Group, k.Topic, true
	case 2:
		var k kmsg.GroupMetadataKey
		if err := k.ReadFrom(key); err != nil {
			return "", "", false
		}
		return k.Group, "", true
	default:
		return "", "", false
	}
}

// ConsumerOffsetsAvailable reports whether the __consumer_offsets topic can be read on
// this cluster, gating the Phase 4 tail exactly as TxnStateAvailable gates the reader.
func ConsumerOffsetsAvailable(ctx context.Context, admin *kadm.Client, topic string) (bool, error) {
	return internalTopicReadable(ctx, admin, topic)
}
