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
	// AbortedBatches counts batches dropped because their producer's
	// transaction was rolled back.
	AbortedBatches int64
	// DecodeErrors counts broker-supplied structures this partition could not
	// parse.
	DecodeErrors int64
}

// Stats is the aggregate view across every assigned partition.
type Stats struct {
	// PartitionsAssigned is how many partitions the tail took on.
	PartitionsAssigned int
	// PartitionsRunning is how many of those still have a live fetch loop.
	PartitionsRunning int
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
	Partitions   []PartitionStats
}

// Tail reads a set of topics continuously, one fetch loop per partition,
// fanning every decoded batch into a single channel.
type Tail struct {
	client Client
	opts   Options

	wg    sync.WaitGroup
	out   chan Batch
	parts []*partitionState
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
	abortedBatches   int64
	decodeErrors     int64
}

func (p *partitionState) snapshot() PartitionStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PartitionStats{
		Topic:            p.topic,
		Partition:        p.partition,
		NextOffset:       p.nextOffset,
		LastStableOffset: p.lastStableOffset,
		HighWaterMark:    p.highWaterMark,
		Lag:              nonNegative(p.lastStableOffset - p.nextOffset),
		OpenTxnBacklog:   nonNegative(p.highWaterMark - p.lastStableOffset),
		Running:          p.running,
		LastAdvance:      p.lastAdvance,
		AbortedBatches:   p.abortedBatches,
		DecodeErrors:     p.decodeErrors,
	}
}

// setRunning records whether this partition's loop is alive.
func (p *partitionState) setRunning(v bool) {
	p.mu.Lock()
	p.running = v
	p.mu.Unlock()
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
		s.Lag += ps.Lag
		s.OpenTxnBacklog += ps.OpenTxnBacklog
		s.AbortedBatches += ps.AbortedBatches
		s.DecodeErrors += ps.DecodeErrors
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
	for {
		if ctx.Err() != nil {
			return
		}
		leader, err := t.client.Leader(a.topic, a.partition)
		if err != nil {
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
		resp, err := t.client.Fetch(leader, spec)
		if err != nil {
			continue
		}
		block := resp.GetBlock(a.topic, a.partition)
		if block == nil {
			continue
		}

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
			if !t.emit(ctx, batch) {
				return
			}
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
