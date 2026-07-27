package discovery

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccumulator_RepeatedObservationsUnionAndDeduplicateTopics(t *testing.T) {
	// A transaction is observed many times across a run — the coordinator rewrites its
	// state record on every status change — and each sighting reports the footprint as
	// of that moment. The footprint that matters is the union of all of them, deduped.
	acc := NewAccumulator()

	acc.Add(Observation{TxnID: "app-0", Topics: []string{"a", "b"}})
	acc.Add(Observation{TxnID: "app-0", Topics: []string{"b", "c", "__consumer_offsets"}})

	snap := acc.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, "app-0", snap[0].TxnID)
	assert.Equal(t, []string{"__consumer_offsets", "a", "b", "c"}, snap[0].Topics)
	assert.Equal(t, 2, snap[0].Samples)
}

func TestAccumulator_SeparateTransactionsStaySeparate(t *testing.T) {
	// Two unrelated transactions must not pool their topics: the whole grouping stage
	// downstream reads a merge here as a coupling that the cluster never had.
	acc := NewAccumulator()

	acc.Add(Observation{TxnID: "x", Topics: []string{"a"}})
	acc.Add(Observation{TxnID: "y", Topics: []string{"b"}})

	snap := acc.Snapshot()
	require.Len(t, snap, 2)
	byID := map[string][]string{snap[0].TxnID: snap[0].Topics, snap[1].TxnID: snap[1].Topics}
	assert.Equal(t, map[string][]string{"x": {"a"}, "y": {"b"}}, byID)
}

func TestAccumulator_SnapshotIsSortedByTransactionalID(t *testing.T) {
	// The artifacts written from a snapshot must not churn between runs over identical
	// observations. Go randomises map iteration per range, so the ordering is asserted
	// across repeated snapshots rather than a single one.
	acc := NewAccumulator()
	for _, id := range []string{"tx-9", "tx-3", "tx-7", "tx-1", "tx-5", "tx-8", "tx-2", "tx-6", "tx-4"} {
		acc.Add(Observation{TxnID: id, Topics: []string{"t"}})
	}
	want := []string{"tx-1", "tx-2", "tx-3", "tx-4", "tx-5", "tx-6", "tx-7", "tx-8", "tx-9"}

	for range 20 {
		var got []string
		for _, fp := range acc.Snapshot() {
			got = append(got, fp.TxnID)
		}
		require.Equal(t, want, got)
	}
}

func TestAccumulator_ObservationsFromDifferentSourcesCreditTheSameTransaction(t *testing.T) {
	// The pipeline contract that lets an enrichment phase fold a recovered consumed
	// input into the write-side footprint: two sources reporting the same transactional
	// id union into one footprint, with both sources credited.
	acc := NewAccumulator()

	acc.Add(Observation{TxnID: "t", Topics: []string{"out"}, Source: SourceTxnStateLog})
	acc.Add(Observation{TxnID: "t", Topics: []string{"in"}, Source: SourceConsumerGroups})

	snap := acc.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, []string{"in", "out"}, snap[0].Topics)
	assert.Equal(t, []string{SourceConsumerGroups, SourceTxnStateLog}, snap[0].Sources)
}

func TestAccumulator_ZeroProducerIDDoesNotOverwriteARecordedOne(t *testing.T) {
	// The naming-based enrichment phase emits observations carrying no producer id.
	// Those must not erase the real id the __transaction_state reader recorded: it is
	// the only key the __consumer_offsets phase can correlate an offset commit on, so
	// clobbering it silently drops every exact recovery for that transaction.
	acc := NewAccumulator()

	acc.Add(Observation{TxnID: "t", ProducerID: 42, Topics: []string{"out"}, Source: SourceTxnStateLog})
	acc.Add(Observation{TxnID: "t", ProducerID: 0, Topics: []string{"in"}, Source: SourceConsumerGroups})

	snap := acc.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, int64(42), snap[0].ProducerID)
}

func TestAccumulator_OnlyAPositiveProducerIDIsRecorded(t *testing.T) {
	// Abuse case: Kafka's no-producer sentinel is -1, not 0. Recording it would put a
	// bogus id in the artifact and — worse — occupy the slot, so the real id decoded
	// from a later record never lands.
	acc := NewAccumulator()

	acc.Add(Observation{TxnID: "t", ProducerID: -1, Topics: []string{"a"}, Source: SourceConsumerOffsets})
	acc.Add(Observation{TxnID: "t", ProducerID: 9, Topics: []string{"a"}, Source: SourceTxnStateLog})

	snap := acc.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, int64(9), snap[0].ProducerID)
}

func TestAccumulator_ReadProcessWriteIsStickyOnceSet(t *testing.T) {
	// A transaction that once committed consumer offsets is a consume-transform-produce
	// app for the rest of the run. Later sightings routinely omit the flag — a completed
	// transaction's footprint is cleared by the coordinator, and enrichment observations
	// carry only recovered inputs — and must not downgrade it. Losing the flag hides
	// that the group's consumed inputs are absent from the footprint, which is exactly
	// the case that breaks exactly-once delivery at cutover.
	acc := NewAccumulator()

	acc.Add(Observation{TxnID: "t", Topics: []string{"a"}, ReadProcessWrite: true, Source: SourceTxnStateLog})
	acc.Add(Observation{TxnID: "t", Topics: []string{"b"}, ReadProcessWrite: false, Source: SourceTxnStateLog})

	snap := acc.Snapshot()
	require.Len(t, snap, 1)
	assert.True(t, snap[0].ReadProcessWrite)
}

func TestAccumulator_AddReportsExactlyTheTopicsItAdded(t *testing.T) {
	// The audit trail exists so an operator can ask "why are these two topics in one
	// group?" and get back the transaction and the phase that coupled them. Only the
	// accumulator knows whether a sighting grew the set, so Add reports the delta —
	// the topics this observation introduced — alongside the resulting full set.
	acc := NewAccumulator()
	acc.Add(Observation{TxnID: "t", Topics: []string{"a"}, Source: SourceTxnStateLog})

	ch := acc.Add(Observation{TxnID: "t", Topics: []string{"c", "b", "a"}, Source: SourceTxnStateLog})

	assert.True(t, ch.Grew())
	assert.Equal(t, []string{"b", "c"}, ch.Added, "only the topics not already in the set, sorted")
	assert.Equal(t, []string{"a", "b", "c"}, ch.Topics, "the resulting full set")
}

func TestAccumulator_AddReportsNoAdditionsWhenTheTopicsAreAlreadyPresent(t *testing.T) {
	// A transaction is re-observed on every coordinator state change, so most sightings
	// report a set already recorded. Those must report no growth, or the audit trail
	// becomes one line per sighting and stops being a record of what coupled what.
	// A topic another source already reported is not growth either: the set that
	// matters is the union, which is what the grouping stage consumes.
	acc := NewAccumulator()
	acc.Add(Observation{TxnID: "t", Topics: []string{"a", "b"}, Source: SourceTxnStateLog})

	repeat := acc.Add(Observation{TxnID: "t", Topics: []string{"b", "a"}, Source: SourceTxnStateLog})
	otherSource := acc.Add(Observation{TxnID: "t", Topics: []string{"a"}, Source: SourceConsumerGroups})

	assert.False(t, repeat.Grew())
	assert.Empty(t, repeat.Added)
	assert.Equal(t, []string{"a", "b"}, repeat.Topics, "the full set is still reported")

	assert.False(t, otherSource.Grew(), "a topic another source already reported is not growth")
	assert.Empty(t, otherSource.Added)
}

func TestAccumulator_ObservationWithAnEmptyTransactionalIDIsIgnored(t *testing.T) {
	// Abuse case: a broker record that decodes to an empty transactional id — malformed,
	// truncated, or a key shape the decoder does not model — must not open a keyless
	// bucket. Every such record would pool into one synthetic transaction, chaining
	// unrelated topics into a single group, and would emit audit lines attributing
	// couplings to a transaction that does not exist.
	acc := NewAccumulator()

	ch := acc.Add(Observation{TxnID: "", Topics: []string{"a", "b"}, Source: SourceTxnStateLog})

	assert.False(t, ch.Grew())
	assert.Empty(t, ch.Added)
	assert.Empty(t, ch.Topics)
	assert.Empty(t, acc.Snapshot())
}

func TestAccumulator_TracksTheObservationWindowIgnoringOutOfOrderSightings(t *testing.T) {
	// FirstSeen/LastSeen bound when a transaction was live during the run. The sources
	// run concurrently and their observations interleave, so an Add can arrive carrying
	// an earlier timestamp than one already merged; that must not drag LastSeen back.
	acc := NewAccumulator()
	t0 := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)

	acc.Add(Observation{TxnID: "t", Topics: []string{"a"}, ObservedAt: t0.Add(1 * time.Minute)})
	acc.Add(Observation{TxnID: "t", Topics: []string{"b"}, ObservedAt: t0.Add(5 * time.Minute)})
	acc.Add(Observation{TxnID: "t", Topics: []string{"c"}, ObservedAt: t0.Add(2 * time.Minute)})

	snap := acc.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, t0.Add(1*time.Minute), snap[0].FirstSeen)
	assert.Equal(t, t0.Add(5*time.Minute), snap[0].LastSeen)
}

func TestAccumulator_ConcurrentAddsReportEachTopicAsAddedExactlyOnce(t *testing.T) {
	// Three observation sources feed one accumulator concurrently, and the audit line is
	// written from the delta Add returns. If two concurrent Adds both claimed the same
	// topic the trace would record a coupling twice; if the check-and-insert were not
	// atomic, neither would claim it and the coupling would be missing from the trace
	// altogether. Every goroutine reports the same topics so they contend on each one.
	// Run under -race.
	const goroutines, topicsEach = 8, 50
	sources := []string{SourceTxnStateLog, SourceConsumerGroups, SourceConsumerOffsets}
	acc := NewAccumulator()

	var (
		mu       sync.Mutex
		reported []string
		wg       sync.WaitGroup
	)
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range topicsEach {
				ch := acc.Add(Observation{
					TxnID:      "t",
					Topics:     []string{fmt.Sprintf("topic-%02d", i)},
					Source:     sources[g%len(sources)],
					ObservedAt: time.Now(),
				})
				mu.Lock()
				reported = append(reported, ch.Added...)
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()

	snap := acc.Snapshot()
	require.Len(t, snap, 1)
	require.Len(t, snap[0].Topics, topicsEach)
	assert.Equal(t, goroutines*topicsEach, snap[0].Samples)

	sort.Strings(reported)
	assert.Equal(t, snap[0].Topics, reported,
		"every topic in the final set must have been reported as added exactly once")
}
