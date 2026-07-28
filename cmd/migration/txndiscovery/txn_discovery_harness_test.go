package txndiscovery

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/discovery"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/report"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
	"github.com/stretchr/testify/require"
)

// --- wire-format fixture builders -------------------------------------------
//
// Hand-assembled bytes against Kafka's TransactionLogKey/Value and
// OffsetCommitKey schemas rather than an encoder from the packages under test,
// so a decoder that only agrees with itself still fails here.

func be16(v int16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(v))
	return b
}

func be32(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

func be64(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

func kstr(s string) []byte { return append(be16(int16(len(s))), s...) }

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// txnKey encodes a __transaction_state record key at schema v0.
func txnKey(txnID string) []byte { return concat(be16(0), kstr(txnID)) }

// txnValue encodes a __transaction_state record value at schema v0, status
// Ongoing (1), with one partition per named topic.
func txnValue(producerID int64, topics ...string) []byte {
	parts := [][]byte{
		be16(0),
		be64(producerID),
		be16(1),
		be32(60000),
		{1}, // Ongoing — a footprint-bearing status
		be32(int32(len(topics))),
	}
	for _, tp := range topics {
		parts = append(parts, kstr(tp), be32(1), be32(0))
	}
	parts = append(parts, be64(1000), be64(900))
	return concat(parts...)
}

// commitKey encodes an OffsetCommitKey v1 — what an exactly-once app's
// sendOffsetsToTransaction writes for one consumed topic-partition.
func commitKey(group, topic string, partition int32) []byte {
	return concat(be16(1), kstr(group), kstr(topic), be32(partition))
}

// stateRecordBatch is a non-transactional batch of transaction-state records.
func stateRecordBatch(firstOffset int64, keys, values [][]byte) *sarama.RecordBatch {
	rb := &sarama.RecordBatch{
		Version:         2,
		FirstOffset:     firstOffset,
		LastOffsetDelta: int32(len(keys) - 1),
		ProducerID:      -1,
	}
	for i := range keys {
		rb.Records = append(rb.Records, &sarama.Record{
			OffsetDelta: int64(i),
			Key:         keys[i],
			Value:       values[i],
		})
	}
	return rb
}

// commitRecordBatch is the transactional batch a transactional offset commit
// arrives in: the header carries the producer id that ties it to its transaction.
func commitRecordBatch(firstOffset, producerID int64, keys ...[]byte) *sarama.RecordBatch {
	rb := &sarama.RecordBatch{
		Version:         2,
		FirstOffset:     firstOffset,
		LastOffsetDelta: int32(len(keys) - 1),
		ProducerID:      producerID,
		ProducerEpoch:   1,
		IsTransactional: true,
	}
	for i, k := range keys {
		rb.Records = append(rb.Records, &sarama.Record{
			OffsetDelta: int64(i),
			Key:         k,
			Value:       []byte{0, 1},
		})
	}
	return rb
}

// --- fakes -------------------------------------------------------------------

// fakeTailClient serves a scripted set of record batches once per topic, then
// answers empty. One partition per topic keeps the assignment predictable.
type fakeTailClient struct {
	mu      sync.Mutex
	scripts map[string][]*sarama.RecordBatch
	served  map[string]bool
	fetches map[string]int
}

func newFakeTailClient() *fakeTailClient {
	return &fakeTailClient{
		scripts: map[string][]*sarama.RecordBatch{},
		served:  map[string]bool{},
		fetches: map[string]int{},
	}
}

func (f *fakeTailClient) script(topic string, batches ...*sarama.RecordBatch) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[topic] = append(f.scripts[topic], batches...)
}

func (f *fakeTailClient) Partitions(string) ([]int32, error) { return []int32{0}, nil }

func (f *fakeTailClient) Offset(string, int32, tail.StartPosition) (int64, error) { return 0, nil }

func (f *fakeTailClient) Leader(string, int32) (tail.Leader, error) {
	return tail.Leader{ID: 1, Addr: "broker:9092", FetchVersion: 11}, nil
}

func (f *fakeTailClient) RefreshMetadata(...string) error { return nil }

func (f *fakeTailClient) Fetch(_ tail.Leader, spec tail.FetchSpec) (*sarama.FetchResponse, error) {
	r := &sarama.FetchResponse{Version: 11}
	r.AddError(spec.Topic, spec.Partition, sarama.ErrNoError)

	f.mu.Lock()
	f.fetches[spec.Topic]++
	batches := f.scripts[spec.Topic]
	first := !f.served[spec.Topic]
	f.served[spec.Topic] = true
	f.mu.Unlock()

	var records int64
	for _, rb := range batches {
		records += int64(len(rb.Records))
	}
	if first {
		for _, rb := range batches {
			blk := r.GetBlock(spec.Topic, spec.Partition)
			blk.RecordsSet = append(blk.RecordsSet, &sarama.Records{RecordBatch: rb})
		}
	}
	// The reader is caught up once it has read the script, so lag is zero and
	// the health line can report OK.
	r.SetLastStableOffset(spec.Topic, spec.Partition, records)
	r.GetBlock(spec.Topic, spec.Partition).HighWaterMarkOffset = records
	return r, nil
}

// fetchesFor reports how many times a topic's partition has been fetched.
func (f *fakeTailClient) fetchesFor(topic string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetches[topic]
}

// fakeGroupAdmin is the consumer-group slice of sarama.ClusterAdmin.
type fakeGroupAdmin struct {
	groups  map[string]string
	offsets map[string][]string // group -> consumed topics
	err     error
}

func (f *fakeGroupAdmin) ListConsumerGroups() (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.groups, nil
}

func (f *fakeGroupAdmin) ListConsumerGroupOffsets(group string, _ map[string][]int32) (*sarama.OffsetFetchResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	resp := &sarama.OffsetFetchResponse{Blocks: map[string]map[int32]*sarama.OffsetFetchResponseBlock{}}
	for _, topic := range f.offsets[group] {
		resp.Blocks[topic] = map[int32]*sarama.OffsetFetchResponseBlock{
			0: {Offset: 10},
		}
	}
	return resp, nil
}

// harness assembles the fakes into the cluster surface the runner connects to.
type harness struct {
	tailClient *fakeTailClient
	describer  *fakeDescriber
	admin      *fakeGroupAdmin
	probeErr   error

	connectErr error
	closes     int
}

func newHarness() *harness {
	return &harness{
		tailClient: newFakeTailClient(),
		describer:  &fakeDescriber{md: metadataFor(discovery.DefaultTxnStateTopic, sarama.ErrNoError, 1)},
		admin:      &fakeGroupAdmin{groups: map[string]string{}, offsets: map[string][]string{}},
	}
}

func (h *harness) connect(Opts) (*cluster, error) {
	if h.connectErr != nil {
		return nil, h.connectErr
	}
	c := &cluster{
		Describer: h.describer,
		Tail:      h.tailClient,
		OffsetsProbe: func(context.Context) error {
			return h.probeErr
		},
		Close: func() error { h.closes++; return nil },
	}
	// Assigned only when there is one, so a nil admin reaches the runner as a nil
	// INTERFACE rather than as a non-nil interface holding a nil pointer. That is
	// the shape connectSarama produces when the enrichment connection could not be
	// opened, and the difference decides whether the run degrades or panics.
	if h.admin != nil {
		c.Admin = h.admin
	}
	return c, nil
}

// runner builds a Runner over the harness whose observation window closes once
// the tail has fetched every scripted topic several times over.
//
// The window is tied to observed progress rather than to a sleep: a partition
// loop cannot issue its next fetch until every batch from the previous one has
// been accepted downstream, so a topic fetched repeatedly after its script was
// served has certainly delivered it.
func (h *harness) runner(t *testing.T, opts Opts) *Runner {
	t.Helper()
	if opts.Stdout == nil {
		opts.Stdout = &bytes.Buffer{}
	}
	// Only topics the run will actually tail: a probe failure keeps the
	// consumer-offsets log out of the assignment, so waiting on its fetch count
	// would wait for a fetch that never comes.
	topics := []string{opts.TxnStateTopic}
	if opts.TailConsumerOffsets && h.probeErr == nil {
		topics = append(topics, discovery.DefaultConsumerOffsetsTopic)
	}
	return &Runner{
		opts:     opts,
		connect:  h.connect,
		newAudit: report.NewAuditWriter,
		window: func(ctx context.Context, _ time.Duration) {
			deadline := time.Now().Add(30 * time.Second)
			for {
				if ctx.Err() != nil {
					return
				}
				settled := true
				for _, topic := range topics {
					if h.tailClient.fetchesFor(topic) < 5 {
						settled = false
					}
				}
				if settled || time.Now().After(deadline) {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		},
	}
}

// baseOpts is a complete run configuration writing into dir.
func baseOpts(dir string) Opts {
	return Opts{
		Brokers:              []string{"broker:9092"},
		Auth:                 authResolution{},
		Duration:             time.Minute,
		Interval:             50 * time.Millisecond,
		TxnStateTopic:        discovery.DefaultTxnStateTopic,
		EnrichConsumerGroups: true,
		TailConsumerOffsets:  true,
		OutPath:              filepath.Join(dir, "txn-discovery.yaml"),
		AuditLogPath:         filepath.Join(dir, DefaultAuditBasename),
		Stdout:               &bytes.Buffer{},
	}
}

// readFile is a require-wrapped os.ReadFile.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// exists reports whether a path is present on disk.
func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}
