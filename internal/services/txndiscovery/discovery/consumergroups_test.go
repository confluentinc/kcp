package discovery

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
)

// The seam has to be satisfiable by the real thing: U11 injects a
// sarama.NewClusterAdminFromClient over the same client the tail holds. This fails to
// compile if either signature drifts from sarama's.
var _ ConsumerGroupAdmin = (sarama.ClusterAdmin)(nil)

// fakeGroupAdmin stands in for sarama's ClusterAdmin at the ConsumerGroupAdmin seam.
type fakeGroupAdmin struct {
	mu sync.Mutex

	groups   map[string]string                      // group id -> protocol type
	offsets  map[string]*sarama.OffsetFetchResponse // group id -> committed offsets
	listErr  []error                                // consumed one per pass; nil means success
	fetchErr map[string]error                       // group id -> error from the offsets call

	listCalls  int
	fetchCalls []string
}

func (f *fakeGroupAdmin) ListConsumerGroups() (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if len(f.listErr) > 0 {
		err := f.listErr[0]
		f.listErr = f.listErr[1:]
		if err != nil {
			return nil, err
		}
	}
	return f.groups, nil
}

func (f *fakeGroupAdmin) ListConsumerGroupOffsets(group string, _ map[string][]int32) (*sarama.OffsetFetchResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchCalls = append(f.fetchCalls, group)
	if err := f.fetchErr[group]; err != nil {
		return nil, err
	}
	resp, ok := f.offsets[group]
	if !ok {
		return offsetFetch(nil), nil
	}
	return resp, nil
}

func (f *fakeGroupAdmin) calls() (list int, fetch []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls, append([]string(nil), f.fetchCalls...)
}

// syncBuffer is a log sink safe to read from the test goroutine while Run's goroutine
// writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newTestEnricher(admin ConsumerGroupAdmin, catalog *TxnCatalog, logTo *syncBuffer) *ConsumerGroupEnricher {
	return &ConsumerGroupEnricher{
		Admin:    admin,
		Catalog:  catalog,
		Interval: time.Hour,
		Log:      slog.New(slog.NewTextHandler(logTo, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// offsetFetch builds the shape sarama's ClusterAdmin returns for a group's committed
// offsets: one block per topic partition.
func offsetFetch(topicPartitions map[string][]int32) *sarama.OffsetFetchResponse {
	resp := &sarama.OffsetFetchResponse{
		Blocks: make(map[string]map[int32]*sarama.OffsetFetchResponseBlock, len(topicPartitions)),
	}
	for topic, partitions := range topicPartitions {
		blocks := make(map[int32]*sarama.OffsetFetchResponseBlock, len(partitions))
		for _, p := range partitions {
			blocks[p] = &sarama.OffsetFetchResponseBlock{Offset: 42}
		}
		resp.Blocks[topic] = blocks
	}
	return resp
}

func TestCorrelateByStreamsConvention_ExactNameMatches(t *testing.T) {
	txnIDs := []string{"analytics-a", "payments-processor", "audit"}

	got := correlateByStreamsConvention("payments-processor", txnIDs)

	want := []string{"payments-processor"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("correlateByStreamsConvention = %v, want %v", got, want)
	}
}

// Kafka Streams derives transactional.id from application.id (== group.id) by
// appending a taskId under EOS-v1 ("<app>-<taskId>") or a processId under EOS-v2
// ("<app>-<processId>"). Both suffix forms must correlate back to the group.
func TestCorrelateByStreamsConvention_EOSSuffixFormsMatch(t *testing.T) {
	txnIDs := []string{
		"payments-processor-0_0",   // EOS-v1 taskId suffix
		"payments-processor-abc12", // EOS-v2 processId suffix
		"analytics-a",
	}

	got := correlateByStreamsConvention("payments-processor", txnIDs)

	want := []string{"payments-processor-0_0", "payments-processor-abc12"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("correlateByStreamsConvention = %v, want %v", got, want)
	}
}

// A bare strings.HasPrefix(id, group) correlates "payments-processor2" — a
// DIFFERENT application — to the "payments-processor" group, folding one app's
// input topics into another app's transaction and coupling two unrelated
// workloads into one migration group. The convention appends "-<suffix>", so the
// separator is part of the prefix.
func TestCorrelateByStreamsConvention_SharedPrefixWithoutHyphenBoundaryDoesNotMatch(t *testing.T) {
	txnIDs := []string{
		"payments-processor2",      // no "-" boundary: a different application
		"payments-processorX-0_0",  // no "-" boundary before the suffix either
		"payments-processor-abc12", // genuine EOS-v2 suffix
	}

	got := correlateByStreamsConvention("payments-processor", txnIDs)

	want := []string{"payments-processor-abc12"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("correlateByStreamsConvention = %v, want %v", got, want)
	}
}

// Abuse case. The group id comes from the broker, and an empty one is reachable:
// pre-2.2 simple consumers committed offsets under group.id "". With group == ""
// the boundary prefix degenerates to "-", which matches EVERY transactional id
// beginning with a hyphen — ids that share not one character of application name
// with the group. That group's committed topics would then be folded into
// unrelated tenants' transactions. An empty group correlates to nothing.
func TestCorrelateByStreamsConvention_EmptyGroupIDCorrelatesToNothing(t *testing.T) {
	txnIDs := []string{"-payments-abc12", "-analytics", "payments-processor"}

	got := correlateByStreamsConvention("", txnIDs)

	if len(got) != 0 {
		t.Errorf("empty group id correlated to %v, want no matches", got)
	}
}

// The topics a group has committed offsets for are exactly the topics it consumes.
// Each is named once regardless of how many partitions it committed, and the order
// is stable — sarama hands back a map, whose iteration order is not.
func TestConsumedTopics_NamesEachCommittedTopicOnceInSortedOrder(t *testing.T) {
	resp := offsetFetch(map[string][]int32{
		"payments.requests": {0, 1, 2},
		"audit.events":      {0},
	})

	got := consumedTopics(resp)

	want := []string{"audit.events", "payments.requests"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("consumedTopics = %v, want %v", got, want)
	}
}

// An EOS group commits its offsets THROUGH the transaction, so __consumer_offsets
// shows up among its committed topics. Reporting it as a consumed input would make
// every EOS group on the cluster share a topic, chaining unrelated workloads into
// one migration group.
func TestConsumedTopics_DropsInternalTopics(t *testing.T) {
	resp := offsetFetch(map[string][]int32{
		"payments.requests":  {0, 1},
		"__consumer_offsets": {12},
	})

	got := consumedTopics(resp)

	want := []string{"payments.requests"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("consumedTopics = %v, want %v", got, want)
	}
}

// One pass over the cluster: for every consumer group that correlates to a known
// transactional id, the topics that group consumes become an observation against
// that transaction. Groups that correlate to nothing cost no offsets call.
func TestConsumerGroupEnricher_EmitsConsumedTopicsForEachCorrelatedTransaction(t *testing.T) {
	catalog := NewTxnCatalog()
	catalog.Observe("payments-processor-abc12", 7)
	catalog.Observe("analytics-aggregator-1", 9)

	admin := &fakeGroupAdmin{
		groups: map[string]string{"payments-processor": "consumer"},
		offsets: map[string]*sarama.OffsetFetchResponse{
			"payments-processor": offsetFetch(map[string][]int32{"payments.requests": {0, 1}}),
		},
	}
	e := newTestEnricher(admin, catalog, &syncBuffer{})

	out := make(chan Observation, 4)
	if err := e.enrich(context.Background(), out); err != nil {
		t.Fatalf("enrich returned %v, want nil", err)
	}
	close(out)

	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d observations, want 1: %+v", len(got), got)
	}
	if got[0].TxnID != "payments-processor-abc12" {
		t.Errorf("observation TxnID = %q, want the correlated transactional id", got[0].TxnID)
	}
	if want := []string{"payments.requests"}; !reflect.DeepEqual(got[0].Topics, want) {
		t.Errorf("observation Topics = %v, want %v", got[0].Topics, want)
	}
	if got[0].Source != SourceConsumerGroups {
		t.Errorf("observation Source = %q, want %q", got[0].Source, SourceConsumerGroups)
	}
	if _, fetched := admin.calls(); !reflect.DeepEqual(fetched, []string{"payments-processor"}) {
		t.Errorf("offsets fetched for %v, want only the correlated group", fetched)
	}
}

// An observation window shorter than the enrichment interval must still enrich, so
// the first pass runs on entry rather than on the first tick.
func TestConsumerGroupEnricher_RunEnrichesImmediatelyNotOnTheFirstTick(t *testing.T) {
	catalog := NewTxnCatalog()
	catalog.Observe("payments-processor-abc12", 7)

	admin := &fakeGroupAdmin{
		groups: map[string]string{"payments-processor": "consumer"},
		offsets: map[string]*sarama.OffsetFetchResponse{
			"payments-processor": offsetFetch(map[string][]int32{"payments.requests": {0}}),
		},
	}
	e := newTestEnricher(admin, catalog, &syncBuffer{}) // Interval is an hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := make(chan Observation, 4)
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx, out) }()

	select {
	case obs := <-out:
		if obs.TxnID != "payments-processor-abc12" {
			t.Errorf("observation TxnID = %q, want the correlated transactional id", obs.TxnID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no observation within 2s: the first pass waited for the interval")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

// Abuse case. Until the __transaction_state reader has decoded something there is
// nothing to correlate against, so a pass can only produce zero observations. Making
// the admin calls anyway means every tick issues a ListGroups plus one OffsetFetch per
// group against a production cluster for a run measured in hours — load with no
// possible result. The catalog check comes before the first admin call.
func TestConsumerGroupEnricher_EmptyCatalogMakesNoAdminCall(t *testing.T) {
	admin := &fakeGroupAdmin{
		groups: map[string]string{"payments-processor": "consumer"},
		offsets: map[string]*sarama.OffsetFetchResponse{
			"payments-processor": offsetFetch(map[string][]int32{"payments.requests": {0}}),
		},
	}
	e := newTestEnricher(admin, NewTxnCatalog(), &syncBuffer{}) // catalog is empty
	e.Interval = time.Millisecond                               // sweep hard

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Observation, 64)
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx, out) }()

	time.Sleep(50 * time.Millisecond) // many ticks
	cancel()
	<-done

	listed, fetched := admin.calls()
	if listed != 0 || len(fetched) != 0 {
		t.Errorf("made %d ListConsumerGroups and %d offset calls against an empty catalog, want 0 and 0", listed, len(fetched))
	}
	if len(out) != 0 {
		t.Errorf("emitted %d observations from an empty catalog, want 0", len(out))
	}
}

// A transactional id the catalog gains LATE in the window must still have its
// consumed input recovered, so the window's end triggers one final pass.
//
// This is the pipeline's timing reality, not a hypothetical. The catalog is filled by
// the __transaction_state reader, which works from the beginning of a 50-partition
// compacted log, so a transaction's id routinely does not reach the catalog until tens
// of seconds into the window — and the enricher's own admin calls share one sarama
// Broker connection with that reader's fetches, so a pass takes seconds rather than
// milliseconds. The deciding pass therefore straddles the end of the window: it
// correlates the group correctly and then loses the race between its send and the
// cancelled context, so the recovery is computed and thrown away. R8's recovery then
// silently does not happen and the app's input topic is reported as individually
// migratable — the exactly-once break the phase exists to prevent.
//
// The __consumer_offsets phase has the identical problem and solves it with a final
// flush on a fresh context (see ConsumerOffsetsTail.FinalFlush, "most resolutions land
// here"). This is that same contract for this phase.
func TestConsumerGroupEnricher_FinalPassDeliversACorrelationTheCatalogGainedLate(t *testing.T) {
	// Correlates to no group, so the first pass reaches the admin — which is the
	// barrier that proves the late arrival really is late — and emits nothing.
	catalog := NewTxnCatalog()
	catalog.Observe("unrelated-workload-abc12", 3)

	admin := &fakeGroupAdmin{
		groups: map[string]string{"payments-processor": "consumer"},
		offsets: map[string]*sarama.OffsetFetchResponse{
			"payments-processor": offsetFetch(map[string][]int32{"payments.requests": {0, 1}}),
		},
	}
	e := newTestEnricher(admin, catalog, &syncBuffer{}) // Interval is an hour: no tick will fire

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan Observation, 4)
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx, out) }()

	// Wait for the immediate pass to have been and gone, so the transaction below
	// cannot be picked up by it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if listed, _ := admin.calls(); listed >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the immediate pass never reached the admin, so the late arrival below would not be late")
		}
		time.Sleep(time.Millisecond)
	}

	// The reader decodes the Streams transaction late in the window.
	catalog.Observe("payments-processor-abc12", 7)

	// The window closes.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	close(out)
	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	if len(got) != 1 {
		t.Fatalf("delivered %d observations, want 1: the correlation the catalog gained late in the window was never emitted, so the consumed input is lost: %+v", len(got), got)
	}
	if got[0].TxnID != "payments-processor-abc12" {
		t.Errorf("observation TxnID = %q, want the late-arriving correlated transactional id", got[0].TxnID)
	}
	if want := []string{"payments.requests"}; !reflect.DeepEqual(got[0].Topics, want) {
		t.Errorf("observation Topics = %v, want %v", got[0].Topics, want)
	}
	if got[0].Source != SourceConsumerGroups {
		t.Errorf("observation Source = %q, want %q", got[0].Source, SourceConsumerGroups)
	}
}

// A coordinator moving, a rebalance, or a momentary ACL problem fails one pass. That
// must not end enrichment for the rest of the window, and it must not vanish either:
// the operator needs the failure in the log to know the phase was degraded.
func TestConsumerGroupEnricher_FailedPassIsLoggedAndTheNextPassStillRuns(t *testing.T) {
	catalog := NewTxnCatalog()
	catalog.Observe("payments-processor-abc12", 7)

	admin := &fakeGroupAdmin{
		groups: map[string]string{"payments-processor": "consumer"},
		offsets: map[string]*sarama.OffsetFetchResponse{
			"payments-processor": offsetFetch(map[string][]int32{"payments.requests": {0}}),
		},
		listErr: []error{errors.New("coordinator not available")}, // first pass only
	}
	logs := &syncBuffer{}
	e := newTestEnricher(admin, catalog, logs)
	e.Interval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Observation, 16)
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx, out) }()

	select {
	case obs := <-out:
		if obs.TxnID != "payments-processor-abc12" {
			t.Errorf("observation TxnID = %q, want the correlated transactional id", obs.TxnID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no observation after the failed pass: enrichment stopped instead of retrying")
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v, want nil — a failed pass must not propagate", err)
	}

	if listed, _ := admin.calls(); listed < 2 {
		t.Errorf("made %d ListConsumerGroups calls, want at least 2 (the failure plus a retry)", listed)
	}
	if !strings.Contains(logs.String(), "coordinator not available") {
		t.Errorf("failed pass was not logged; log was:\n%s", logs.String())
	}
}

// The pipeline contract, against a real Accumulator rather than a mock. The
// __transaction_state reader supplies the PRODUCED topics and the real producer id;
// enrichment supplies the CONSUMED input under the same transactional id. They must
// union into one footprint marked read-process-write — that flag is what tells the
// report the group's inputs came from a recovery phase rather than the footprint.
//
// The producer id is the second half of the contract. Enrichment has no producer id,
// so it emits 0; the accumulator's positive-only guard is what stops that 0 from
// erasing the real id, which is the only key the __consumer_offsets phase can join a
// transactional offset commit on. Losing it here would silently disable that phase.
func TestConsumerGroupEnricher_RecoveredInputFoldsIntoTheProducedFootprint(t *testing.T) {
	const (
		txnID          = "payments-processor-abc12"
		realProducerID = int64(5000)
	)

	catalog := NewTxnCatalog()
	catalog.Observe(txnID, realProducerID)

	acc := NewAccumulator()
	acc.Add(Observation{
		TxnID:            txnID,
		ProducerID:       realProducerID,
		Topics:           []string{"payments.approved", "payments.ledger", "__consumer_offsets"},
		ReadProcessWrite: true,
		Source:           SourceTxnStateLog,
		ObservedAt:       time.Now(),
	})

	admin := &fakeGroupAdmin{
		groups: map[string]string{"payments-processor": "consumer"},
		offsets: map[string]*sarama.OffsetFetchResponse{
			"payments-processor": offsetFetch(map[string][]int32{
				"payments.requests":  {0, 1},
				"__consumer_offsets": {12},
			}),
		},
	}
	e := newTestEnricher(admin, catalog, &syncBuffer{})

	out := make(chan Observation, 4)
	if err := e.enrich(context.Background(), out); err != nil {
		t.Fatalf("enrich returned %v, want nil", err)
	}
	close(out)
	for obs := range out {
		if !obs.ReadProcessWrite {
			t.Error("enrichment observation is not marked read-process-write")
		}
		if obs.ProducerID != 0 {
			t.Errorf("enrichment observation carries ProducerID %d, want 0 — it has no producer id to report", obs.ProducerID)
		}
		acc.Add(obs)
	}

	snap := acc.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("accumulated %d footprints, want 1", len(snap))
	}
	got := snap[0]
	wantTopics := []string{
		"__consumer_offsets",
		"payments.approved",
		"payments.ledger",
		"payments.requests",
	}
	if !reflect.DeepEqual(got.Topics, wantTopics) {
		t.Errorf("merged footprint = %v, want %v (the consumed input must union with the produced set)", got.Topics, wantTopics)
	}
	if !got.ReadProcessWrite {
		t.Error("merged footprint is not read-process-write")
	}
	if got.ProducerID != realProducerID {
		t.Errorf("footprint ProducerID = %d, want %d — enrichment's zero id clobbered the real one", got.ProducerID, realProducerID)
	}
	if want := []string{SourceConsumerGroups, SourceTxnStateLog}; !reflect.DeepEqual(got.Sources, want) {
		t.Errorf("footprint Sources = %v, want %v", got.Sources, want)
	}
}

// A group rebalancing mid-pass fails its own offsets call. Abandoning the whole pass
// would make one busy application's rebalance rate decide whether every other
// application on the cluster ever gets enriched.
func TestConsumerGroupEnricher_GroupWithUnreadableOffsetsIsSkippedAndThePassContinues(t *testing.T) {
	catalog := NewTxnCatalog()
	catalog.Observe("payments-processor-abc12", 7)
	catalog.Observe("analytics-aggregator-0_0", 8)

	admin := &fakeGroupAdmin{
		groups: map[string]string{
			"payments-processor":   "consumer",
			"analytics-aggregator": "consumer",
		},
		offsets: map[string]*sarama.OffsetFetchResponse{
			"analytics-aggregator": offsetFetch(map[string][]int32{"analytics.raw": {0}}),
		},
		fetchErr: map[string]error{"payments-processor": errors.New("rebalance in progress")},
	}
	logs := &syncBuffer{}
	e := newTestEnricher(admin, catalog, logs)

	out := make(chan Observation, 8)
	if err := e.enrich(context.Background(), out); err != nil {
		t.Fatalf("enrich returned %v, want nil — one group's failure is not the pass's failure", err)
	}
	close(out)

	var got []Observation
	for obs := range out {
		got = append(got, obs)
	}
	if len(got) != 1 {
		t.Fatalf("emitted %d observations, want 1 (the group that could be read): %+v", len(got), got)
	}
	if got[0].TxnID != "analytics-aggregator-0_0" {
		t.Errorf("observation TxnID = %q, want the readable group's transaction", got[0].TxnID)
	}
	if !strings.Contains(logs.String(), "rebalance in progress") {
		t.Errorf("skipped group was not logged; log was:\n%s", logs.String())
	}
}

// kcp.log is unconditionally Debug+ and is the file operators attach to support
// tickets, so a consumer group id, topic name, or transactional id in ANY log line
// leaks customer business structure regardless of level — and under --verbose those
// same lines reach the console. Both of this phase's log paths are driven here, with
// distinctively-named fixtures, and neither may name anything.
func TestConsumerGroupEnricher_LogLinesNameNoGroupTopicOrTransaction(t *testing.T) {
	const (
		groupID = "qxgroupqx"
		txnID   = "qxgroupqx-abc12"
		topic   = "qxtopicqx"
	)

	catalog := NewTxnCatalog()
	catalog.Observe(txnID, 7)

	admin := &fakeGroupAdmin{
		groups:  map[string]string{groupID: "consumer"},
		offsets: map[string]*sarama.OffsetFetchResponse{groupID: offsetFetch(map[string][]int32{topic: {0}})},
		// First pass fails at the listing; every later pass reaches — and fails at —
		// the per-group offsets call, so both log paths fire.
		listErr:  []error{errors.New("listing blew up")},
		fetchErr: map[string]error{groupID: errors.New("offsets blew up")},
	}
	logs := &syncBuffer{}
	e := newTestEnricher(admin, catalog, logs)
	e.Interval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Observation, 16)
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx, out) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	got := logs.String()
	// Both paths must actually have run, or the absence assertions below prove nothing.
	for _, marker := range []string{"listing blew up", "offsets blew up"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("log path %q never fired, so this test would pass vacuously; log was:\n%s", marker, got)
		}
	}
	for name, kind := range map[string]string{
		groupID: "consumer group id",
		txnID:   "transactional id",
		topic:   "topic name",
	} {
		if strings.Contains(got, name) {
			t.Errorf("log leaked a %s (%q); log was:\n%s", kind, name, got)
		}
	}
}
