// Package tail is a continuous, resumable reader over raw sarama Broker.Fetch.
//
// sarama's ConsumerMessage discards the record-batch header, so the high-level
// consumer cannot see the producer id that ties a transactional offset commit
// to its transaction. The raw fetch path exposes it, at the cost of owning
// everything the high-level consumer would have handled: partition discovery,
// leader resolution, offset tracking, failover, aborted-transaction filtering
// and lag accounting.
package tail

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/IBM/sarama"
)

// Record is one fully decoded record from a fetched batch.
type Record struct {
	Offset    int64
	Key       []byte
	Value     []byte
	Timestamp time.Time
}

// Batch is one record batch delivered to a consumer with its header intact —
// the producer id, transactional flag and control flag the high-level consumer
// would have discarded.
type Batch struct {
	Topic           string
	Partition       int32
	Leader          int32
	BaseOffset      int64
	ProducerID      int64
	ProducerEpoch   int16
	IsTransactional bool
	Control         bool
	Records         []Record
}

// Options configures a Tail. The zero value is usable: every field falls back
// to its package default.
type Options struct {
	// MaxWaitTime bounds how long a broker holds a fetch open waiting for
	// data. Together with the client's read timeout it bounds shutdown
	// latency, because an in-flight fetch cannot be cancelled.
	MaxWaitTime time.Duration
	// IdleBackoff is the pause after a fetch that returned no records.
	IdleBackoff time.Duration
	// BackoffBase is the first retry wait after a failed fetch. It doubles on
	// each consecutive failure and resets on the next success.
	BackoffBase time.Duration
	// BackoffMax caps the retry wait, so a long outage does not turn into an
	// unbounded pause that outlives the observation window.
	BackoffMax time.Duration
	// MaxConcurrentFetches caps how many fetches are in flight at once across
	// every partition. It bounds both the load put on the source cluster and
	// peak memory, which is this cap times MaxPartitionBytes.
	MaxConcurrentFetches int
	// Sleep is the injectable wait used for backoff and idle polling. Tests
	// replace it so the suite never waits on a real timer.
	Sleep func(ctx context.Context, d time.Duration)
}

// New builds a Tail over the given seam.
func New(c Client, opts Options) *Tail {
	if opts.MaxWaitTime <= 0 {
		opts.MaxWaitTime = DefaultMaxWaitTime
	}
	if opts.IdleBackoff <= 0 {
		opts.IdleBackoff = DefaultIdleBackoff
	}
	if opts.BackoffBase <= 0 {
		opts.BackoffBase = DefaultBackoffBase
	}
	if opts.BackoffMax <= 0 {
		opts.BackoffMax = DefaultBackoffMax
	}
	if opts.MaxConcurrentFetches <= 0 {
		opts.MaxConcurrentFetches = DefaultMaxConcurrentFetches
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepCtx
	}
	return &Tail{client: c, opts: opts}
}

// Default tuning constants.
const (
	// DefaultMaxWaitTime is how long a broker may hold a fetch open. It is the
	// dominant term in shutdown latency, since an in-flight fetch cannot be
	// cancelled.
	DefaultMaxWaitTime = 500 * time.Millisecond
	// DefaultMinBytes is 1 so the broker returns as soon as any record is
	// available rather than batching up to MaxWaitTime.
	DefaultMinBytes int32 = 1
	// DefaultMaxPartitionBytes bounds one partition's share of a response.
	DefaultMaxPartitionBytes int32 = 1 << 20
	// DefaultIdleBackoff is the pause after a fetch that returned nothing. A
	// real broker already blocked for MaxWaitTime, so this only stops a
	// hot loop when the broker answers immediately.
	DefaultIdleBackoff = 50 * time.Millisecond
	// DefaultBackoffBase is the first retry wait after a failed fetch. It is
	// short because the common failure — a leader move — resolves as soon as
	// the metadata refresh lands.
	DefaultBackoffBase = 100 * time.Millisecond
	// DefaultBackoffMax caps the retry wait. Five seconds keeps a partition
	// responsive within a run measured in minutes while still throttling a
	// broker that is down for a long time.
	DefaultBackoffMax = 5 * time.Second
	// DefaultMaxConcurrentFetches caps in-flight fetches across all
	// partitions. The two internal topics default to 50 partitions each, so
	// uncapped this would be 100 simultaneous long polls against a customer's
	// production cluster. Sixteen bounds peak memory at 16 x
	// MaxPartitionBytes (16 MiB by default) and still cycles 100 partitions
	// roughly every MaxWaitTime x 100/16 — a few seconds, which is ample for
	// a run measured in minutes to hours.
	DefaultMaxConcurrentFetches = 16
)

// PartitionStats reports one partition's progress.
type PartitionStats struct {
	Topic            string
	Partition        int32
	NextOffset       int64
	LastStableOffset int64
	HighWaterMark    int64
	// Lag is LastStableOffset minus NextOffset (KTD9). Under ReadCommitted the
	// broker returns nothing past the last stable offset, so measuring against
	// the high watermark instead would show a permanent floor whenever any
	// transaction is open.
	Lag int64
	// OpenTxnBacklog is HighWaterMark minus LastStableOffset: records written
	// inside transactions the coordinator has not yet decided. It is reported
	// separately because it is not something the reader can catch up on.
	OpenTxnBacklog int64
	// Running reports whether this partition's fetch loop is still alive. A
	// loop that exited reads as zero lag and is otherwise indistinguishable
	// from a healthy idle one.
	Running bool
	// LastAdvance is when this partition's offset last moved, zero if it never
	// has. A stalled partition reports zero lag, so this is the only signal
	// separating it from a genuinely caught-up one.
	LastAdvance time.Time
	// RecordsRead counts records delivered from this partition. Records
	// dropped by the abort filter are counted in AbortedBatches instead.
	RecordsRead int64
	// AbortedBatches counts batches dropped because their producer's
	// transaction was rolled back.
	AbortedBatches int64
	// DecodeErrors counts broker-supplied structures this partition could not
	// parse.
	DecodeErrors int64
	// LeadershipErrors counts fetches rejected because this partition's leader
	// moved.
	LeadershipErrors int64
	// UnclassifiedErrors counts error codes with no specific recovery.
	UnclassifiedErrors int64
	// OffsetResets counts reseeks to this partition's log start.
	OffsetResets int64
	// TransportErrors counts fetches that failed without a usable response.
	TransportErrors int64
	// LastError is the most recent fetch failure on this partition, empty if
	// there has not been one. It is what surfaces an unrecognised code.
	LastError string
}

// Stats is the aggregate view across every assigned partition.
type Stats struct {
	// PartitionsAssigned is how many partitions the tail took on.
	PartitionsAssigned int
	// PartitionsRunning is how many of those still have a live fetch loop.
	PartitionsRunning int
	// RecordsRead is the total number of records delivered to consumers.
	RecordsRead int64
	// Lag is the sum of the per-partition last-stable-offset lags.
	Lag int64
	// OpenTxnBacklog is the sum of the per-partition high-watermark-to-LSO gaps.
	OpenTxnBacklog int64
	// AbortedBatches counts batches dropped because their producer's
	// transaction was rolled back.
	AbortedBatches int64
	// DecodeErrors counts broker-supplied structures this reader could not
	// parse. It is the format-drift alarm, so it is never swallowed.
	DecodeErrors int64
	// LeadershipErrors counts fetches rejected because the partition's leader
	// moved, each of which forced a metadata refresh and a retry.
	LeadershipErrors int64
	// UnclassifiedErrors counts Kafka error codes this reader has no specific
	// recovery for. They are backed off and surfaced, never fatal.
	UnclassifiedErrors int64
	// OffsetResets counts reseeks to the log start after the reader's position
	// fell out of the retained range.
	OffsetResets int64
	// TransportErrors counts fetches that failed without a response at all,
	// which is what a broker restart looks like from here.
	TransportErrors int64
	Partitions      []PartitionStats
}

// Tail reads a set of topics continuously, one fetch loop per partition,
// fanning every decoded batch into a single channel.
type Tail struct {
	client Client
	opts   Options

	wg    sync.WaitGroup
	out   chan Batch
	parts []*partitionState
	// sem bounds in-flight fetches across every partition loop.
	sem chan struct{}
}

// fetch performs one fetch round-trip, holding a concurrency slot for its
// duration. It reports ok false when the context ended before a slot came
// free, so the caller stops rather than queueing behind a shutdown.
func (t *Tail) fetch(ctx context.Context, leader Leader, spec FetchSpec) (resp *sarama.FetchResponse, err error, ok bool) {
	select {
	case t.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, false
	}
	defer func() { <-t.sem }()
	resp, err = t.client.Fetch(leader, spec)
	return resp, err, true
}

// partitionState is one partition's mutable progress, read concurrently by
// Stats while its loop advances it.
type partitionState struct {
	mu               sync.Mutex
	topic            string
	partition        int32
	nextOffset       int64
	lastStableOffset int64
	highWaterMark    int64
	running          bool
	lastAdvance      time.Time
	recordsRead      int64
	abortedBatches   int64
	decodeErrors     int64
	leadershipErrors int64

	unclassifiedErrors int64
	offsetResets       int64
	transportErrors    int64
	lastError          string
}

func (p *partitionState) snapshot() PartitionStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PartitionStats{
		Topic:              p.topic,
		Partition:          p.partition,
		NextOffset:         p.nextOffset,
		LastStableOffset:   p.lastStableOffset,
		HighWaterMark:      p.highWaterMark,
		Lag:                nonNegative(p.lastStableOffset - p.nextOffset),
		OpenTxnBacklog:     nonNegative(p.highWaterMark - p.lastStableOffset),
		Running:            p.running,
		LastAdvance:        p.lastAdvance,
		RecordsRead:        p.recordsRead,
		AbortedBatches:     p.abortedBatches,
		DecodeErrors:       p.decodeErrors,
		LeadershipErrors:   p.leadershipErrors,
		UnclassifiedErrors: p.unclassifiedErrors,
		OffsetResets:       p.offsetResets,
		TransportErrors:    p.transportErrors,
		LastError:          p.lastError,
	}
}

// setRunning records whether this partition's loop is alive.
func (p *partitionState) setRunning(v bool) {
	p.mu.Lock()
	p.running = v
	p.mu.Unlock()
}

// record applies a mutation under the partition's lock.
func (p *partitionState) record(fn func(*partitionState)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

// nonNegative clamps a computed lag at zero. A negative value is meaningless
// and would corrupt the aggregate.
func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// Stats returns a point-in-time snapshot of every partition's progress.
func (t *Tail) Stats() Stats {
	s := Stats{
		PartitionsAssigned: len(t.parts),
		Partitions:         make([]PartitionStats, 0, len(t.parts)),
	}
	for _, p := range t.parts {
		ps := p.snapshot()
		if ps.Running {
			s.PartitionsRunning++
		}
		s.RecordsRead += ps.RecordsRead
		s.Lag += ps.Lag
		s.OpenTxnBacklog += ps.OpenTxnBacklog
		s.AbortedBatches += ps.AbortedBatches
		s.DecodeErrors += ps.DecodeErrors
		s.LeadershipErrors += ps.LeadershipErrors
		s.UnclassifiedErrors += ps.UnclassifiedErrors
		s.OffsetResets += ps.OffsetResets
		s.TransportErrors += ps.TransportErrors
		s.Partitions = append(s.Partitions, ps)
	}
	return s
}

// Start resolves the assignment for every topic, spawns one fetch loop per
// partition, and returns the channel they fan into. The channel closes once
// every loop has exited, which happens when ctx is done.
func (t *Tail) Start(ctx context.Context, topics []TopicSpec) (<-chan Batch, error) {
	assignments, err := discover(t.client, topics)
	if err != nil {
		return nil, err
	}

	t.out = make(chan Batch)
	t.sem = make(chan struct{}, t.opts.MaxConcurrentFetches)
	for _, a := range assignments {
		st := &partitionState{topic: a.topic, partition: a.partition, nextOffset: a.offset, running: true}
		t.parts = append(t.parts, st)
		t.wg.Add(1)
		go func(a assignment, st *partitionState) {
			defer t.wg.Done()
			t.runPartition(ctx, a, st)
		}(a, st)
	}
	go func() {
		t.wg.Wait()
		close(t.out)
	}()
	return t.out, nil
}

// Wait blocks until every partition loop has exited.
func (t *Tail) Wait() { t.wg.Wait() }

// assignment is one partition to read and where to start.
type assignment struct {
	topic     string
	partition int32
	offset    int64
}

// discover resolves each topic's partitions and their start offsets.
func discover(c Client, topics []TopicSpec) ([]assignment, error) {
	var out []assignment
	for _, ts := range topics {
		parts, err := c.Partitions(ts.Topic)
		if err != nil {
			return nil, fmt.Errorf("failed to list partitions: %w", err)
		}
		sorted := append([]int32(nil), parts...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		for _, p := range sorted {
			off, err := c.Offset(ts.Topic, p, ts.Start)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve start offset for partition %d: %w", p, err)
			}
			out = append(out, assignment{topic: ts.Topic, partition: p, offset: off})
		}
	}
	return out, nil
}

// runPartition is the per-partition fetch loop.
func (t *Tail) runPartition(ctx context.Context, a assignment, st *partitionState) {
	defer st.setRunning(false)

	offset := a.offset
	// attempt counts consecutive failures; it drives the backoff curve and
	// resets on the next fetch that comes back clean.
	attempt := 0
	retry := func() {
		t.opts.Sleep(ctx, backoffFor(t.opts.BackoffBase, t.opts.BackoffMax, attempt))
		attempt++
	}

	for {
		// Cancellation is checked here and nowhere else in the request path:
		// sarama.Broker.Fetch takes no context, so an in-flight request is
		// bounded by MaxWaitTime plus the client's read timeout rather than
		// abandoned. What this guarantees is that no *new* request is issued
		// once the context is done.
		if ctx.Err() != nil {
			return
		}
		leader, err := t.client.Leader(a.topic, a.partition)
		if err != nil {
			st.record(func(p *partitionState) {
				p.transportErrors++
				p.lastError = err.Error()
			})
			retry()
			continue
		}
		spec := FetchSpec{
			Topic:             a.topic,
			Partition:         a.partition,
			Offset:            offset,
			Version:           leader.FetchVersion,
			MaxWaitMS:         int32(t.opts.MaxWaitTime / time.Millisecond),
			MinBytes:          DefaultMinBytes,
			MaxBytes:          DefaultMaxPartitionBytes,
			MaxPartitionBytes: DefaultMaxPartitionBytes,
			Isolation:         sarama.ReadCommitted,
		}
		resp, err, ok := t.fetch(ctx, leader, spec)
		if !ok {
			return
		}
		if err != nil {
			// No response at all, so no error code to classify. This is what a
			// broker restart looks like from here.
			st.record(func(p *partitionState) {
				p.transportErrors++
				p.lastError = err.Error()
			})
			retry()
			continue
		}
		block := resp.GetBlock(a.topic, a.partition)
		if block == nil {
			// A response that answers the request but omits the partition is
			// malformed. The broker is untrusted input, so this is counted and
			// retried rather than dereferenced.
			st.record(func(p *partitionState) {
				p.transportErrors++
				p.lastError = "fetch response omitted the requested partition"
			})
			retry()
			continue
		}
		if block.Err != sarama.ErrNoError {
			next, backoff := t.recover(a, st, block.Err, offset)
			offset = next
			if backoff {
				retry()
			} else {
				attempt = 0
			}
			continue
		}
		attempt = 0

		next := offset
		emitted := false
		aborted := newAbortTracker(block.AbortedTransactions)
		for _, records := range block.RecordsSet {
			batch, last, ok := decodeBatch(a, leader, records.RecordBatch)
			if !ok {
				continue
			}
			aborted.advanceTo(records.RecordBatch.LastOffset())
			if batch.Control {
				// A control batch is the transaction marker itself, never
				// aborted data. An abort marker ends its producer's rolled-back
				// transaction, so the producer's next one must not be filtered.
				typ, cerr := controlRecordType(records.RecordBatch)
				switch {
				case cerr != nil:
					// The marker is unreadable, so whether this producer's
					// transaction aborted or committed is unknown. Leaving it
					// in the aborted set keeps filtering rather than crediting
					// possibly rolled-back records as real.
					st.mu.Lock()
					st.decodeErrors++
					st.mu.Unlock()
				case typ == sarama.ControlRecordAbort:
					aborted.clear(batch.ProducerID)
				}
			} else if batch.IsTransactional && aborted.isAborted(batch.ProducerID) {
				// Skipped, but still consumed: the offset advances past the
				// records that were decoded, or the loop refetches them.
				st.mu.Lock()
				st.abortedBatches++
				st.mu.Unlock()
				next = last + 1
				continue
			}
			delivered := int64(len(batch.Records))
			if !t.emit(ctx, batch) {
				return
			}
			st.record(func(p *partitionState) { p.recordsRead += delivered })
			emitted = true
			next = last + 1
		}
		advanced := next > offset
		offset = next
		st.mu.Lock()
		st.nextOffset = offset
		st.lastStableOffset = block.LastStableOffset
		st.highWaterMark = block.HighWaterMarkOffset
		if advanced {
			st.lastAdvance = time.Now()
		}
		st.mu.Unlock()

		if !emitted {
			// A real broker already held the fetch open for MaxWaitTime, so
			// this only matters when it answered immediately; without it the
			// loop hot-spins on an idle partition.
			t.opts.Sleep(ctx, t.opts.IdleBackoff)
		}
	}
}

// backoffFor returns the wait before retry number attempt, counting from zero:
// base, then doubling, capped at max. Deterministic rather than jittered —
// there is one reader per partition, not a thundering herd to spread out.
func backoffFor(base, max time.Duration, attempt int) time.Duration {
	d := base
	for i := 0; i < attempt && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

// recover applies the recovery for one classified fetch error, returning the
// offset to fetch from next and whether the caller should back off first. It
// never signals an exit: a partition loop that gives up silently is the
// failure mode this component exists to avoid.
func (t *Tail) recover(a assignment, st *partitionState, kerr sarama.KError, offset int64) (int64, bool) {
	switch classifyKafkaError(kerr) {
	case classLeadership:
		// The leader moved. Refresh so the next Leader call resolves the new
		// one, then retry from the same offset.
		st.record(func(p *partitionState) {
			p.leadershipErrors++
			p.lastError = kerr.Error()
		})
		if rerr := t.client.RefreshMetadata(a.topic); rerr != nil {
			slog.Debug("⏭️ metadata refresh after a leadership error failed", "partition", a.partition, "error", rerr)
		}
		return offset, true

	case classOffset:
		// The remembered position fell outside the retained range, so retrying
		// it would loop on a dead offset forever. Reseek to the log start.
		earliest, oerr := t.client.Offset(a.topic, a.partition, StartEarliest)
		if oerr != nil {
			slog.Debug("⏭️ could not resolve the earliest offset for a reseek", "partition", a.partition, "error", oerr)
			return offset, true
		}
		st.record(func(p *partitionState) {
			p.offsetResets++
			p.nextOffset = earliest
		})
		slog.Warn("⚠️ fetch offset is outside the retained range; reseeking to the log start",
			"partition", a.partition, "from", offset, "to", earliest)
		return earliest, false

	default:
		// An error code with no specific recovery. Counting and surfacing it
		// is the whole point: a loop that exited here would read downstream as
		// a partition that simply had no data.
		st.record(func(p *partitionState) {
			p.unclassifiedErrors++
			p.lastError = kerr.Error()
		})
		slog.Warn("⚠️ unrecognised Kafka error while fetching; backing off and retrying",
			"partition", a.partition, "code", int16(kerr))
		return offset, true
	}
}

// decodeBatch turns a sarama RecordBatch into a Batch, reporting the offset of
// the last fully decoded record. ok is false when the batch carried no
// decodable record.
func decodeBatch(a assignment, leader Leader, rb *sarama.RecordBatch) (Batch, int64, bool) {
	// A pre-0.11 message set decodes with RecordBatch nil. It cannot carry a
	// producer id, so there is nothing here to correlate; skip it.
	if rb == nil {
		return Batch{}, 0, false
	}
	b := Batch{
		Topic:           a.topic,
		Partition:       a.partition,
		Leader:          leader.ID,
		BaseOffset:      rb.FirstOffset,
		ProducerID:      rb.ProducerID,
		ProducerEpoch:   rb.ProducerEpoch,
		IsTransactional: rb.IsTransactional,
		Control:         rb.Control,
	}
	var last int64
	for _, r := range rb.Records {
		if r == nil {
			continue
		}
		off := rb.FirstOffset + r.OffsetDelta
		b.Records = append(b.Records, Record{
			Offset:    off,
			Key:       r.Key,
			Value:     r.Value,
			Timestamp: rb.FirstTimestamp.Add(r.TimestampDelta),
		})
		last = off
	}
	if len(b.Records) == 0 {
		return b, 0, false
	}
	// last, not rb.LastOffset(): PartialTrailingRecord means the batch was cut
	// at the fetch boundary, so the header's last offset covers records that
	// were never decoded. Advancing past them would silently skip records and
	// drop topics from a transaction's footprint.
	return b, last, true
}

// emit hands a batch to the consumer, reporting false when ctx is done.
func (t *Tail) emit(ctx context.Context, b Batch) bool {
	// A consumer that has stopped reading must not pin the loop open, or
	// shutdown deadlocks behind an unread send.
	select {
	case t.out <- b:
		return true
	case <-ctx.Done():
		return false
	}
}

// sleepCtx is the default Sleep: a context-aware pause.
func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
