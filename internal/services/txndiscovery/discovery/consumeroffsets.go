package discovery

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/txnlog"
)

// finalFlushTimeout bounds the fresh context the final flush runs on.
const finalFlushTimeout = 10 * time.Second

// DefaultMaxPendingProducers caps how many unresolved producer ids the pending
// buffer holds.
//
// The buffer only ever holds producers whose transaction has NOT been seen, and
// on a large cluster that is most of them: their state records were compacted
// before the window opened, or the transaction simply never recurs. Unbounded,
// it therefore grows for the whole run, which on a multi-hour observation loses
// the run rather than a request.
//
// Twenty thousand entries is roughly an order of magnitude above the producer
// count of a large exactly-once estate, so a healthy cluster never reaches it
// and the cap only engages on the pathological case it exists for. Each entry
// is a map header plus the topic set one consumer group commits offsets for —
// a few hundred bytes — so the cap bounds this buffer in the low tens of MiB.
const DefaultMaxPendingProducers = 20000

// DefaultResolveInterval is how often buffered sightings are resolved against
// the catalog. Resolution is a map lookup per pending entry, so the cadence is
// set by how promptly a recovery should reach the audit trail rather than by
// cost.
const DefaultResolveInterval = 30 * time.Second

// DefaultPendingTTLIntervals is how many resolve intervals a sighting may go
// unresolved before it is dropped.
//
// The entry cap alone only engages once the buffer is full, which on a
// moderately busy cluster may be never — so without a TTL a run keeps every
// sighting from its first minute for hours. Ten intervals is deliberately
// generous: the __transaction_state reader starts at the log's beginning and
// can take many minutes to reach the present on a large partition, and every
// sighting dropped before it gets there is a recovery lost.
const DefaultPendingTTLIntervals = 10

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

	keyDecodeErrors atomic.Int64

	maxPending int
	interval   time.Duration
	pendingTTL time.Duration
	now        func() time.Time

	mu sync.Mutex
	// pending holds sightings not yet resolved to a transaction: producer id ->
	// the consumed topics it committed offsets for. An entry is removed once its
	// producer id resolves and is emitted, or when it is evicted.
	pending map[int64]*pendingCommits
	// order lists pending entries oldest first, so eviction can drop the
	// longest-waiting without scanning the map. It may hold references to
	// entries that have since resolved or been replaced; seq disambiguates.
	order []pendingRef
	// seq numbers pending entries in creation order.
	seq uint64
	// evicted counts sightings dropped without ever resolving — because the
	// buffer was full, or because they aged out.
	evicted int64
}

// pendingCommits is one producer's unresolved sighting.
type pendingCommits struct {
	topics map[string]struct{}
	// firstSeen is when this producer was first buffered. The TTL runs from
	// here rather than from the last commit, because the question a sighting
	// ages out on is "has its transaction shown up yet?", and a producer that
	// keeps committing without ever being catalogued is exactly the case the
	// buffer must not hold forever.
	firstSeen time.Time
	// seq is this entry's position in creation order. A producer that resolves
	// and then commits again gets a new one, so a stale order entry naming the
	// same producer id cannot evict the fresh sighting.
	seq uint64
}

// pendingRef points at a pending entry as it was when it was created.
type pendingRef struct {
	pid int64
	seq uint64
}

// ConsumerOffsetsOptions configures a ConsumerOffsetsTail. The zero value is
// usable: every field falls back to its package default.
type ConsumerOffsetsOptions struct {
	// Topic is the offsets log to demultiplex out of the shared tail channel.
	// It must match the TopicSpec the tail was started with, so it is set from
	// the same place rather than hardcoded on both sides. Defaults to
	// DefaultConsumerOffsetsTopic.
	Topic string

	// MaxPendingProducers caps how many unresolved producer ids stay buffered.
	// Past it the longest-waiting entries are dropped and counted. Defaults to
	// DefaultMaxPendingProducers.
	MaxPendingProducers int

	// Interval is the cadence at which buffered sightings are resolved against
	// the catalog and flushed. Defaults to DefaultResolveInterval.
	Interval time.Duration

	// PendingTTLIntervals is how many resolve intervals a sighting may go
	// unresolved before it is dropped. Expressed as a multiple of Interval
	// rather than as a duration because the interval is what sets how many
	// chances a sighting gets. Defaults to DefaultPendingTTLIntervals.
	PendingTTLIntervals int

	// Now is the clock, injectable so the suite does not wait on real time.
	// Defaults to time.Now.
	Now func() time.Time
}

// NewConsumerOffsetsTail builds a tail consumer over the shared catalog the
// __transaction_state reader populates.
func NewConsumerOffsetsTail(catalog *TxnCatalog, opts ConsumerOffsetsOptions) *ConsumerOffsetsTail {
	if opts.Topic == "" {
		opts.Topic = DefaultConsumerOffsetsTopic
	}
	if opts.MaxPendingProducers <= 0 {
		opts.MaxPendingProducers = DefaultMaxPendingProducers
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultResolveInterval
	}
	if opts.PendingTTLIntervals <= 0 {
		opts.PendingTTLIntervals = DefaultPendingTTLIntervals
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &ConsumerOffsetsTail{
		catalog:    catalog,
		topic:      opts.Topic,
		maxPending: opts.MaxPendingProducers,
		interval:   opts.Interval,
		pendingTTL: time.Duration(opts.PendingTTLIntervals) * opts.Interval,
		now:        opts.Now,
		pending:    make(map[int64]*pendingCommits),
	}
}

// ConsumerOffsetsStats is what this source contributes to the run's report.
type ConsumerOffsetsStats struct {
	// KeyDecodeErrors counts record keys this source could not parse. Record
	// keys are a broker-internal format rather than part of the stable client
	// protocol, so this is the format-drift alarm and is never swallowed.
	KeyDecodeErrors int64

	// PendingProducers is how many sightings are still waiting for their
	// transaction to appear in the catalog.
	PendingProducers int

	// PendingEvicted counts sightings dropped from the pending buffer without
	// ever resolving. It is reported because it is the only signal that a
	// bounded buffer discarded recoveries: a run that evicted heavily observed
	// far fewer consumed inputs than the cluster actually has.
	PendingEvicted int64
}

// Name reports this source's provenance label.
func (t *ConsumerOffsetsTail) Name() string { return SourceConsumerOffsets }

// Stats returns a snapshot of what this source recovered and how it coped.
func (t *ConsumerOffsetsTail) Stats() ConsumerOffsetsStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return ConsumerOffsetsStats{
		KeyDecodeErrors:  t.keyDecodeErrors.Load(),
		PendingProducers: len(t.pending),
		PendingEvicted:   t.evicted,
	}
}

// Run consumes batches from in until the channel closes, buffering every
// transactional offset commit and resolving the buffer against the catalog on
// the configured interval. When the input ends it performs the final flush.
//
// This mirrors the __transaction_state reader's contract: the command wiring
// owns the tail.Tail, reads its single batch channel, and copies each batch to
// the reader whose topic it names. Run takes a receive-only channel rather than
// a Tail precisely so one tail can be demultiplexed across both readers.
//
// out must be drained for the duration of the run. Run returns nil on both
// ordinary endings — the counters on Stats report whether the read was clean.
func (t *ConsumerOffsetsTail) Run(ctx context.Context, in <-chan tail.Batch, out chan<- Observation) error {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case b, ok := <-in:
			if !ok {
				// The tail has stopped and the fan-out has closed our channel.
				// The final flush runs on a fresh context because ctx is
				// already cancelled by now and this is where most resolutions
				// land.
				t.FinalFlush(out)
				return nil
			}
			t.HandleBatch(b)
		case <-ticker.C:
			t.resolveAndFlush(ctx, out)
		}
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
		// A tombstone — a valid key with an empty value — is how the coordinator
		// DELETES a commit on offset expiry or an admin DeleteOffsets. Its key
		// still names a topic, so nothing downstream would notice; reading it as
		// live would report an input the application no longer consumes.
		if len(r.Value) == 0 {
			continue
		}
		key, err := txnlog.DecodeOffsetKey(r.Key)
		if err != nil {
			// Counted, not fatal, and the rest of the batch is still read: the
			// counter is this source's only format-drift alarm, and abandoning
			// the batch would silently lose the recoveries after the bad key.
			t.keyDecodeErrors.Add(1)
			continue
		}
		// An empty topic is a group-metadata record (key version 2): consumer
		// group state, not an offset commit. It decodes cleanly, so only this
		// check stops a nameless topic reaching a real transaction's group.
		if key.Topic == "" {
			continue
		}
		// An exactly-once app routinely commits offsets for internal topics.
		// Publishing one as a recovered input would give the grouping stage an
		// edge through a topic every such app shares, and would report a topic
		// no operator can migrate in the audit trail and the recovery stats.
		if grouping.IsInternalTopic(key.Topic) {
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

	e := t.pending[pid]
	if e == nil {
		// Only a NEW producer grows the buffer; another commit from one already
		// buffered costs at most one more topic.
		if len(t.pending) >= t.maxPending {
			t.evictOldestLocked()
		}
		t.seq++
		e = &pendingCommits{
			topics:    make(map[string]struct{}),
			firstSeen: t.now(),
			seq:       t.seq,
		}
		t.pending[pid] = e
		t.order = append(t.order, pendingRef{pid: pid, seq: e.seq})
		t.compactOrderLocked()
	}
	e.topics[topic] = struct{}{}
}

// evictOldestLocked drops the longest-waiting pending entry. A sighting that
// has waited longest is the least likely ever to resolve, and dropping the
// newest instead would discard the commits whose transactions the
// __transaction_state reader is about to reach.
func (t *ConsumerOffsetsTail) evictOldestLocked() {
	for len(t.order) > 0 {
		ref := t.order[0]
		t.order = t.order[1:]
		// Skip references to entries that have since resolved, or that were
		// replaced by a later sighting from the same producer.
		if e, ok := t.pending[ref.pid]; ok && e.seq == ref.seq {
			delete(t.pending, ref.pid)
			t.evicted++
			return
		}
	}
}

// compactOrderLocked drops stale references so the order list cannot outgrow
// the buffer it indexes. Resolving an entry leaves its reference behind, so
// over a long run the list would otherwise grow with total sightings rather
// than with the bounded number outstanding.
func (t *ConsumerOffsetsTail) compactOrderLocked() {
	if len(t.order) <= 2*t.maxPending {
		return
	}
	live := make([]pendingRef, 0, len(t.pending))
	for _, ref := range t.order {
		if e, ok := t.pending[ref.pid]; ok && e.seq == ref.seq {
			live = append(live, ref)
		}
	}
	t.order = live
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
	now := t.now()
	observations := t.resolveWith(t.catalog.ProducerIDToTxnID(), now)

	for _, obs := range observations {
		select {
		case out <- obs:
			// Only now, once the observation is actually in the consumer's
			// hands, does the sighting leave the buffer. Dropping it at resolve
			// time would mean a pass cancelled mid-send had both removed the
			// sighting and failed to deliver it: the recovery gone, no counter
			// moving, and the final flush finding nothing left to do.
			t.forget(obs.ProducerID)
		case <-ctx.Done():
			// Abandoned. Everything not yet sent stays buffered for the final
			// flush, which runs on a context that is not already cancelled.
			return
		}
	}

	// Expire AFTER resolving, never before. A sighting that has just reached its
	// TTL may be exactly the one the __transaction_state reader has finally
	// caught up to; expiring first would discard a recovery on the very pass
	// that could have made it, visible only as an eviction count.
	t.expire(now)
}

// forget removes a sighting that has been delivered.
//
// HandleBatch and the resolve passes are serialised by Run's select loop, so no
// commit can land between an entry being resolved and being forgotten here.
func (t *ConsumerOffsetsTail) forget(pid int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, pid)
}

// expire drops pending sightings that have gone unresolved for longer than the
// TTL. Entries are held oldest-first, so the walk stops at the first survivor.
func (t *ConsumerOffsetsTail) expire(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.order) > 0 {
		ref := t.order[0]
		e, ok := t.pending[ref.pid]
		if !ok || e.seq != ref.seq {
			// A stale reference to an entry that resolved or was replaced.
			t.order = t.order[1:]
			continue
		}
		if now.Sub(e.firstSeen) <= t.pendingTTL {
			return
		}
		t.order = t.order[1:]
		delete(t.pending, ref.pid)
		t.evicted++
	}
}

// resolveWith is the pure join at the heart of this unit: every pending
// sighting whose producer id appears in pidToTxn becomes an observation under
// that transaction. It keys purely on the producer id, so it correlates a
// consumer group to its transaction however their names relate.
//
// It does NOT remove what it matched. Observations are returned for the caller
// to send — so sending can respect a context without holding the lock — and the
// caller calls forget once each one has actually been delivered. Removing here
// instead would let a pass cancelled mid-send lose the recovery entirely.
//
// Sorted by transactional id so a pass over identical state emits in a stable
// order, which the audit trail is diffed on.
func (t *ConsumerOffsetsTail) resolveWith(pidToTxn map[int64]string, now time.Time) []Observation {
	t.mu.Lock()
	defer t.mu.Unlock()

	var out []Observation
	for pid, e := range t.pending {
		txnID, ok := pidToTxn[pid]
		if !ok {
			// Its transaction has not been decoded yet — keep waiting. The two
			// readers race by design: this one starts at latest, the
			// __transaction_state reader starts at earliest and takes the whole
			// window to catch up, so a commit routinely arrives first.
			continue
		}
		out = append(out, Observation{
			TxnID:      txnID,
			ProducerID: pid,
			Topics:     sortedKeys(e.topics),
			// A recovered consumed input is by definition an input to a
			// transaction that committed consumer offsets.
			ReadProcessWrite: true,
			Source:           SourceConsumerOffsets,
			ObservedAt:       now,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TxnID < out[j].TxnID })
	return out
}
