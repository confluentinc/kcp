package tail

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fake client (the KTD2 seam) -------------------------------------------

// fakeClient implements Client without a broker. It records every fetch spec
// and every metadata refresh so a test can assert on what the loop asked for,
// and it lets a test move a leader mid-stream, which is the behaviour a
// one-method fake around Fetch cannot express.
type fakeClient struct {
	mu sync.Mutex

	partitions map[string][]int32
	offsets    map[string]int64
	leaders    map[string]Leader

	fetches   []FetchSpec
	refreshes []string

	respond func(spec FetchSpec, call int) (*sarama.FetchResponse, error)
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		partitions: map[string][]int32{},
		offsets:    map[string]int64{},
		leaders:    map[string]Leader{},
	}
}

func offsetKey(topic string, partition int32, pos StartPosition) string {
	return topic + "/" + string(rune('0'+partition)) + "/" + string(rune('0'+int(pos)))
}

func partKey(topic string, partition int32) string {
	return topic + "/" + string(rune('0'+partition))
}

func (f *fakeClient) setPartitions(topic string, parts ...int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partitions[topic] = parts
}

func (f *fakeClient) setOffset(topic string, partition int32, pos StartPosition, offset int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offsets[offsetKey(topic, partition, pos)] = offset
}

func (f *fakeClient) setLeader(topic string, partition int32, l Leader) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaders[partKey(topic, partition)] = l
}

func (f *fakeClient) respondWith(fn func(spec FetchSpec, call int) (*sarama.FetchResponse, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respond = fn
}

func (f *fakeClient) fetchSpecs() []FetchSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FetchSpec(nil), f.fetches...)
}

func (f *fakeClient) refreshedTopics() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.refreshes...)
}

func (f *fakeClient) Partitions(topic string) ([]int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.partitions[topic]
	if !ok {
		return nil, sarama.ErrUnknownTopicOrPartition
	}
	return p, nil
}

func (f *fakeClient) Offset(topic string, partition int32, pos StartPosition) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.offsets[offsetKey(topic, partition, pos)], nil
}

func (f *fakeClient) Leader(topic string, partition int32) (Leader, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.leaders[partKey(topic, partition)]
	if !ok {
		return Leader{}, sarama.ErrLeaderNotAvailable
	}
	return l, nil
}

func (f *fakeClient) RefreshMetadata(topics ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes = append(f.refreshes, topics...)
	return nil
}

func (f *fakeClient) Fetch(leader Leader, spec FetchSpec) (*sarama.FetchResponse, error) {
	f.mu.Lock()
	call := len(f.fetches)
	f.fetches = append(f.fetches, spec)
	respond := f.respond
	f.mu.Unlock()
	if respond == nil {
		return &sarama.FetchResponse{}, nil
	}
	return respond(spec, call)
}

// --- helpers ---------------------------------------------------------------

// testOptions returns Options with every wait replaced by a recorder, so the
// suite never sleeps for a meaningful duration.
func testOptions(sleeps *[]time.Duration) Options {
	var mu sync.Mutex
	return Options{
		Sleep: func(ctx context.Context, d time.Duration) {
			mu.Lock()
			if sleeps != nil {
				*sleeps = append(*sleeps, d)
			}
			mu.Unlock()
		},
	}
}

// singlePartition wires a fake with one topic/partition led by broker 1.
func singlePartition(topic string, start int64) *fakeClient {
	c := newFakeClient()
	c.setPartitions(topic, 0)
	c.setOffset(topic, 0, StartEarliest, start)
	c.setOffset(topic, 0, StartLatest, start)
	c.setLeader(topic, 0, Leader{ID: 1, Addr: "b1:9092", FetchVersion: 11})
	return c
}

// collect drains n batches from out, then cancels and waits for the channel to
// close. It fails the test if the batches do not arrive in time.
func collect(t *testing.T, out <-chan Batch, n int, cancel context.CancelFunc) []Batch {
	t.Helper()
	var got []Batch
	deadline := time.After(5 * time.Second)
	for len(got) < n {
		select {
		case b, ok := <-out:
			if !ok {
				t.Fatalf("channel closed after %d of %d batches", len(got), n)
			}
			got = append(got, b)
		case <-deadline:
			t.Fatalf("timed out after %d of %d batches", len(got), n)
		}
	}
	cancel()
	drain(t, out)
	return got
}

// waitFor blocks until ch is closed, failing the test on timeout.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// afterCall returns a channel closed once the fake has served n fetches, and
// the responder wrapper that closes it.
func afterCall(n int, inner func(spec FetchSpec, call int) (*sarama.FetchResponse, error)) (<-chan struct{}, func(FetchSpec, int) (*sarama.FetchResponse, error)) {
	reached := make(chan struct{})
	var once sync.Once
	return reached, func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		if call+1 >= n {
			once.Do(func() { close(reached) })
		}
		return inner(spec, call)
	}
}

// drain reads until the output channel closes.
func drain(t *testing.T, out <-chan Batch) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the output channel to close")
		}
	}
}

// addBatch appends a hand-built record batch to a response block, so a test
// can control header fields sarama's convenience builders do not expose.
func addBatch(r *sarama.FetchResponse, topic string, partition int32, rb *sarama.RecordBatch) {
	blk := r.GetBlock(topic, partition)
	records := &sarama.Records{RecordBatch: rb}
	blk.RecordsSet = append(blk.RecordsSet, records)
}

// batchOf builds a version-2 record batch carrying one record per value.
func batchOf(firstOffset int64, producerID int64, epoch int16, transactional bool, values ...string) *sarama.RecordBatch {
	rb := &sarama.RecordBatch{
		Version:         2,
		FirstOffset:     firstOffset,
		LastOffsetDelta: int32(len(values) - 1),
		ProducerID:      producerID,
		ProducerEpoch:   epoch,
		IsTransactional: transactional,
	}
	for i, v := range values {
		rb.Records = append(rb.Records, &sarama.Record{OffsetDelta: int64(i), Value: []byte(v)})
	}
	return rb
}

// fetchResponse builds an empty, error-free response block with the given
// last-stable-offset and high watermark.
func fetchResponse(topic string, partition int32, lso, hwm int64) *sarama.FetchResponse {
	r := &sarama.FetchResponse{Version: 11}
	r.AddError(topic, partition, sarama.ErrNoError)
	r.SetLastStableOffset(topic, partition, lso)
	r.GetBlock(topic, partition).HighWaterMarkOffset = hwm
	return r
}

// --- tests -----------------------------------------------------------------

func TestRecordsAreEmittedInOffsetOrderAndTheNextFetchResumesAfterTheLastOffset(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 0)
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		if spec.Offset != 0 {
			return fetchResponse(topic, 0, 3, 3), nil
		}
		r := fetchResponse(topic, 0, 3, 3)
		r.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("a"), 0, 100, true)
		r.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("b"), 1, 100, true)
		r.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("c"), 2, 100, true)
		return r, nil
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)

	got := collect(t, out, 3, cancel)

	var values []string
	var offsets []int64
	for _, b := range got {
		require.Len(t, b.Records, 1)
		values = append(values, string(b.Records[0].Value))
		offsets = append(offsets, b.Records[0].Offset)
	}
	assert.Equal(t, []string{"a", "b", "c"}, values)
	assert.Equal(t, []int64{0, 1, 2}, offsets)

	specs := c.fetchSpecs()
	require.GreaterOrEqual(t, len(specs), 2, "expected a follow-up fetch")
	assert.Equal(t, int64(0), specs[0].Offset)
	assert.Equal(t, int64(3), specs[1].Offset, "the next fetch must resume from the last offset plus one")
}

func TestLagIsLastStableOffsetMinusNextOffsetPerPartitionAndAggregated(t *testing.T) {
	const topic = "t"
	c := newFakeClient()
	c.setPartitions(topic, 0, 1)
	for _, p := range []int32{0, 1} {
		c.setOffset(topic, p, StartEarliest, 0)
		c.setLeader(topic, p, Leader{ID: 1, Addr: "b1:9092", FetchVersion: 11})
	}
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		switch spec.Partition {
		case 0:
			r := fetchResponse(topic, 0, 10, 10)
			if spec.Offset == 0 {
				r.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("a"), 0, 100, true)
				r.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("b"), 1, 100, true)
			}
			return r, nil
		default:
			r := fetchResponse(topic, 1, 5, 5)
			if spec.Offset == 0 {
				r.AddRecordBatch(topic, 1, nil, sarama.ByteEncoder("c"), 0, 100, true)
			}
			return r, nil
		}
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	collect(t, out, 3, cancel)

	stats := tl.Stats()
	require.Len(t, stats.Partitions, 2)
	byPartition := map[int32]PartitionStats{}
	for _, p := range stats.Partitions {
		byPartition[p.Partition] = p
	}
	assert.Equal(t, int64(2), byPartition[0].NextOffset)
	assert.Equal(t, int64(8), byPartition[0].Lag, "partition 0: LSO 10 minus next offset 2")
	assert.Equal(t, int64(1), byPartition[1].NextOffset)
	assert.Equal(t, int64(4), byPartition[1].Lag, "partition 1: LSO 5 minus next offset 1")
	assert.Equal(t, int64(12), stats.Lag, "aggregate lag is the sum of the per-partition lags")
}

func TestAnOpenTransactionReportsAsBacklogNotAsReaderLag(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 0)
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		// The high watermark runs six offsets ahead of the last stable offset:
		// a transaction is open, so a read-committed reader is fully caught up
		// at offset 1 even though the log holds more.
		r := fetchResponse(topic, 0, 1, 7)
		if spec.Offset == 0 {
			r.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("a"), 0, 100, true)
		}
		return r, nil
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	collect(t, out, 1, cancel)

	stats := tl.Stats()
	require.Len(t, stats.Partitions, 1)
	assert.Equal(t, int64(0), stats.Partitions[0].Lag, "a caught-up reader has no lag while a transaction is open")
	assert.Equal(t, int64(6), stats.Partitions[0].OpenTxnBacklog, "the high-watermark-to-LSO gap is open-transaction backlog")
	assert.Equal(t, int64(0), stats.Lag)
	assert.Equal(t, int64(6), stats.OpenTxnBacklog)
}

func TestAnEmptyFetchResponseAdvancesNothingAndDoesNotSpin(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 5)
	reached, responder := afterCall(3, func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		return fetchResponse(topic, 0, 5, 5), nil
	})
	c.respondWith(responder)

	var sleeps []time.Duration
	tl := New(c, testOptions(&sleeps))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	waitFor(t, reached, "three fetches")
	cancel()
	drain(t, out)

	for i, spec := range c.fetchSpecs() {
		assert.Equal(t, int64(5), spec.Offset, "fetch %d must not have advanced past an empty response", i)
	}
	assert.Equal(t, int64(5), tl.Stats().Partitions[0].NextOffset)

	require.NotEmpty(t, sleeps, "an empty response must yield before refetching, or the loop hot-spins")
	for _, d := range sleeps {
		assert.Greater(t, d, time.Duration(0), "the idle yield must be a real pause")
	}
}

func TestALegacyMessageSetIsSkippedWithoutDereferencingNil(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 0)
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		r := fetchResponse(topic, 0, 2, 2)
		if spec.Offset == 0 {
			// A pre-0.11 message set decodes with RecordBatch nil.
			r.AddMessage(topic, 0, nil, sarama.ByteEncoder("legacy"), 0)
			r.AddRecordBatch(topic, 0, nil, sarama.ByteEncoder("modern"), 1, 100, true)
		}
		return r, nil
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 1, cancel)

	require.Len(t, got, 1)
	require.Len(t, got[0].Records, 1)
	assert.Equal(t, "modern", string(got[0].Records[0].Value))
}

func TestTheBatchHeaderReachesTheConsumerIntact(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 0)
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		r := fetchResponse(topic, 0, 3, 3)
		if spec.Offset == 0 {
			addBatch(r, topic, 0, batchOf(0, 4242, 7, true, "a", "b"))
			r.AddControlRecord(topic, 0, 2, 4242, sarama.ControlRecordCommit)
		}
		return r, nil
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 2, cancel)

	require.Len(t, got, 2)
	data := got[0]
	assert.Equal(t, int64(4242), data.ProducerID, "the producer id is the whole reason for the raw fetch path")
	assert.Equal(t, int16(7), data.ProducerEpoch)
	assert.True(t, data.IsTransactional)
	assert.False(t, data.Control)
	assert.Equal(t, int64(0), data.BaseOffset)
	assert.Equal(t, topic, data.Topic)
	assert.Equal(t, int32(0), data.Partition)
	assert.Equal(t, int32(1), data.Leader)

	ctrl := got[1]
	assert.True(t, ctrl.Control, "a transaction marker must reach the consumer flagged as a control batch")
	assert.Equal(t, int64(4242), ctrl.ProducerID)
	assert.True(t, ctrl.IsTransactional)
}

func TestNegotiateFetchVersionClampsToTheBrokerCeilingWithVersion4AsTheFloor(t *testing.T) {
	fetchKey := func(max int16) []sarama.ApiVersionsResponseKey {
		return []sarama.ApiVersionsResponseKey{
			{ApiKey: 0, MinVersion: 0, MaxVersion: 9}, // Produce, to prove the key lookup discriminates
			{ApiKey: fetchAPIKey, MinVersion: 0, MaxVersion: max},
		}
	}

	tests := []struct {
		name      string
		keys      []sarama.ApiVersionsResponseKey
		ceiling   int16
		want      int16
		wantErr   bool
		errSubstr string
	}{
		{name: "broker at or above the ceiling pins the ceiling", keys: fetchKey(13), ceiling: 11, want: 11},
		{name: "broker below the ceiling clamps down", keys: fetchKey(6), ceiling: 11, want: 6},
		{name: "broker exactly at the floor is accepted", keys: fetchKey(4), ceiling: 11, want: 4},
		{name: "a ceiling below the floor is raised to the floor", keys: fetchKey(11), ceiling: 2, want: 4},
		{
			name: "a broker advertising below the floor is an actionable error", keys: fetchKey(3), ceiling: 11,
			wantErr: true, errSubstr: "advertises a maximum Fetch API version of 3",
		},
		{
			name: "a broker that does not advertise Fetch at all is an actionable error",
			keys: []sarama.ApiVersionsResponseKey{{ApiKey: 0, MaxVersion: 9}}, ceiling: 11,
			wantErr: true, errSubstr: "did not advertise the Fetch API",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := negotiateFetchVersion(tt.keys, tt.ceiling)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildFetchRequestCarriesTheNegotiatedVersionAndSessionFields(t *testing.T) {
	spec := FetchSpec{
		Topic:             "t",
		Partition:         3,
		Offset:            77,
		MaxWaitMS:         500,
		MinBytes:          1,
		MaxBytes:          2 << 20,
		MaxPartitionBytes: 1 << 20,
		Isolation:         sarama.ReadCommitted,
	}

	for _, version := range []int16{4, 5, 6, 7, 8, 9, 10, 11} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			s := spec
			s.Version = version
			req := buildFetchRequest(s)

			assert.Equal(t, version, req.Version)
			assert.Equal(t, int32(500), req.MaxWaitTime)
			assert.Equal(t, int32(1), req.MinBytes)
			assert.Equal(t, int32(2<<20), req.MaxBytes, "MaxBytes is encoded from v3 up, and the floor here is v4")
			assert.Equal(t, sarama.ReadCommitted, req.Isolation)

			if version >= 7 {
				// Session id 0 with epoch -1 tells the broker not to open a
				// fetch session, which this reader never uses (KIP-227).
				assert.Equal(t, int32(0), req.SessionID)
				assert.Equal(t, int32(-1), req.SessionEpoch)
			} else {
				assert.Equal(t, int32(0), req.SessionID)
				assert.Equal(t, int32(0), req.SessionEpoch, "the session fields are not part of the wire format below v7")
			}
		})
	}
}

func TestCancellingTheContextStopsEveryPartitionLoopAndClosesTheOutput(t *testing.T) {
	const topic = "t"
	c := newFakeClient()
	c.setPartitions(topic, 0, 1, 2)
	for _, p := range []int32{0, 1, 2} {
		c.setOffset(topic, p, StartEarliest, 0)
		c.setLeader(topic, p, Leader{ID: 1, Addr: "b1:9092", FetchVersion: 11})
	}
	// Every partition always has a record waiting, so every loop is trying to
	// hand a batch to a consumer that never reads one.
	reached, responder := afterCall(3, func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		r := fetchResponse(topic, spec.Partition, spec.Offset+1, spec.Offset+1)
		addBatch(r, topic, spec.Partition, batchOf(spec.Offset, 100, 0, true, "v"))
		return r, nil
	})
	c.respondWith(responder)

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	waitFor(t, reached, "every partition to issue a fetch")

	cancel()

	stopped := make(chan struct{})
	go func() { tl.Wait(); close(stopped) }()
	waitFor(t, stopped, "every partition loop to exit while blocked on an unread send")

	drain(t, out)

	issued := len(c.fetchSpecs())
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, issued, len(c.fetchSpecs()), "no fetch may be issued once the context is done")
}

func TestAPartitionLoopThatExitsIsReportedInTheLivenessCounts(t *testing.T) {
	const topic = "t"
	c := newFakeClient()
	c.setPartitions(topic, 0, 1)
	for _, p := range []int32{0, 1} {
		c.setOffset(topic, p, StartEarliest, 0)
		c.setLeader(topic, p, Leader{ID: 1, Addr: "b1:9092", FetchVersion: 11})
	}
	reached, responder := afterCall(2, func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		return fetchResponse(topic, spec.Partition, 0, 0), nil
	})
	c.respondWith(responder)

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	waitFor(t, reached, "both partitions to fetch")

	running := tl.Stats()
	assert.Equal(t, 2, running.PartitionsAssigned)
	assert.Equal(t, 2, running.PartitionsRunning)

	cancel()
	drain(t, out)

	stopped := tl.Stats()
	assert.Equal(t, 2, stopped.PartitionsAssigned, "an exited partition stays in the assigned count")
	assert.Equal(t, 0, stopped.PartitionsRunning, "a loop that exits must show up as no longer running")
	for _, p := range stopped.Partitions {
		assert.False(t, p.Running, "partition %d must report itself stopped", p.Partition)
	}
}

func TestOnlyAPartitionThatAdvancedRecordsALastAdvanceTime(t *testing.T) {
	const topic = "t"
	c := newFakeClient()
	c.setPartitions(topic, 0, 1)
	for _, p := range []int32{0, 1} {
		c.setOffset(topic, p, StartEarliest, 0)
		c.setLeader(topic, p, Leader{ID: 1, Addr: "b1:9092", FetchVersion: 11})
	}
	// Partition 1 never produces a record: it is caught up at offset 0, so its
	// lag is zero and only a last-advance time distinguishes it from a
	// partition that has silently stopped moving.
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		if spec.Partition == 1 {
			return fetchResponse(topic, 1, 0, 0), nil
		}
		r := fetchResponse(topic, 0, spec.Offset+1, spec.Offset+1)
		addBatch(r, topic, 0, batchOf(spec.Offset, 100, 0, true, "v"))
		return r, nil
	})

	before := time.Now()
	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	collect(t, out, 3, cancel)

	byPartition := map[int32]PartitionStats{}
	for _, p := range tl.Stats().Partitions {
		byPartition[p.Partition] = p
	}
	assert.False(t, byPartition[0].LastAdvance.IsZero(), "an advancing partition must record when it last moved")
	assert.False(t, byPartition[0].LastAdvance.Before(before))
	assert.True(t, byPartition[1].LastAdvance.IsZero(), "a partition that never advanced must not look like it did")
	assert.Equal(t, int64(0), byPartition[1].Lag, "the stalled partition's lag is zero, which is exactly why liveness is needed")
}

// --- abort filtering -------------------------------------------------------

func TestABatchBelongingToAnAbortedTransactionIsNotEmitted(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 0)
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		r := fetchResponse(topic, 0, 2, 2)
		if spec.Offset == 0 {
			// ReadCommitted bounds the fetch at the last stable offset and
			// hands back the aborted list; it does not remove the records.
			r.GetBlock(topic, 0).AbortedTransactions = []*sarama.AbortedTransaction{
				{ProducerID: 500, FirstOffset: 0},
			}
			addBatch(r, topic, 0, batchOf(0, 500, 0, true, "rolled-back"))
			addBatch(r, topic, 0, batchOf(1, 600, 0, true, "committed"))
		}
		return r, nil
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 1, cancel)

	require.Len(t, got, 1)
	assert.Equal(t, int64(600), got[0].ProducerID, "the aborted producer's batch must be dropped, not credited as real")
	assert.Equal(t, "committed", string(got[0].Records[0].Value))
	assert.Equal(t, int64(1), tl.Stats().AbortedBatches, "a dropped batch must be counted, not silently discarded")
}

func TestAnAbortControlBatchClearsItsProducerSoLaterBatchesAreEmitted(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 0)
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		r := fetchResponse(topic, 0, 3, 3)
		if spec.Offset == 0 {
			r.GetBlock(topic, 0).AbortedTransactions = []*sarama.AbortedTransaction{
				{ProducerID: 500, FirstOffset: 0},
			}
			addBatch(r, topic, 0, batchOf(0, 500, 0, true, "rolled-back"))
			r.AddControlRecord(topic, 0, 1, 500, sarama.ControlRecordAbort)
			// Same producer id, next transaction, committed. Leaving 500 in
			// the aborted set would silently drop it.
			addBatch(r, topic, 0, batchOf(2, 500, 1, true, "committed-later"))
		}
		return r, nil
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 2, cancel)

	require.Len(t, got, 2)
	assert.True(t, got[0].Control, "the abort marker itself still reaches the consumer")
	assert.Equal(t, "committed-later", string(got[1].Records[0].Value))
	assert.Equal(t, int64(500), got[1].ProducerID)
	assert.Equal(t, int64(1), tl.Stats().AbortedBatches, "only the batch inside the rolled-back transaction is dropped")
}

// --- partial batches -------------------------------------------------------

func TestAPartialTrailingRecordAdvancesOnlyPastFullyDecodedRecords(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 0)
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		r := fetchResponse(topic, 0, 5, 5)
		switch spec.Offset {
		case 0:
			// The batch header claims offsets 0..4, but the fetch boundary cut
			// it after offset 2. Trusting the header would skip 3 and 4 — and
			// a dropped record is a dropped topic in a transaction footprint.
			rb := batchOf(0, 100, 0, true, "a", "b", "c")
			rb.LastOffsetDelta = 4
			rb.PartialTrailingRecord = true
			addBatch(r, topic, 0, rb)
		case 3:
			addBatch(r, topic, 0, batchOf(3, 100, 0, true, "d", "e"))
		}
		return r, nil
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 2, cancel)

	var offsets []int64
	var values []string
	for _, b := range got {
		for _, rec := range b.Records {
			offsets = append(offsets, rec.Offset)
			values = append(values, string(rec.Value))
		}
	}
	assert.Equal(t, []int64{0, 1, 2, 3, 4}, offsets, "the cut records must be refetched, not skipped")
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, values)

	specs := c.fetchSpecs()
	require.GreaterOrEqual(t, len(specs), 2)
	assert.Equal(t, int64(3), specs[1].Offset, "the refetch resumes at the first record that was cut")
}

// --- decode errors ---------------------------------------------------------

func TestAnUndecodableControlRecordIsCountedAndKeepsFilteringItsProducer(t *testing.T) {
	const topic = "t"
	c := singlePartition(topic, 0)
	c.respondWith(func(spec FetchSpec, call int) (*sarama.FetchResponse, error) {
		r := fetchResponse(topic, 0, 4, 4)
		if spec.Offset == 0 {
			r.GetBlock(topic, 0).AbortedTransactions = []*sarama.AbortedTransaction{
				{ProducerID: 500, FirstOffset: 0},
			}
			addBatch(r, topic, 0, batchOf(0, 500, 0, true, "rolled-back"))

			// A truncated control-record key: two bytes where four are needed.
			// The marker cannot be read, so whether producer 500's transaction
			// aborted or committed is unknown.
			truncated := &sarama.RecordBatch{
				Version: 2, FirstOffset: 1, ProducerID: 500,
				IsTransactional: true, Control: true,
				Records: []*sarama.Record{{OffsetDelta: 0, Key: []byte{0x00, 0x00}}},
			}
			addBatch(r, topic, 0, truncated)

			addBatch(r, topic, 0, batchOf(2, 500, 0, true, "still-suspect"))
			addBatch(r, topic, 0, batchOf(3, 700, 0, true, "unrelated"))
		}
		return r, nil
	})

	tl := New(c, testOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := tl.Start(ctx, []TopicSpec{{Topic: topic, Start: StartEarliest}})
	require.NoError(t, err)
	got := collect(t, out, 2, cancel)

	require.Len(t, got, 2)
	assert.True(t, got[0].Control)
	assert.Equal(t, int64(700), got[1].ProducerID,
		"an unreadable abort marker must not be taken as a commit; producer 500 stays filtered")

	stats := tl.Stats()
	assert.Equal(t, int64(1), stats.DecodeErrors, "format drift must be counted, since it is the drift alarm")
	assert.Equal(t, int64(2), stats.AbortedBatches)
	assert.Equal(t, int64(4), stats.Partitions[0].NextOffset,
		"the offset advances past decoded records only, including the ones that were filtered")
}
