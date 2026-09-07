package discovery

import (
	"reflect"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

func TestCorrelateByStreamsConvention(t *testing.T) {
	txnIDs := []string{
		"payments-processor",       // exact match (rare, but valid)
		"payments-processor-0_0",   // Streams EOS-v1 taskId suffix
		"payments-processor-abc12", // Streams EOS-v2 processId suffix
		"payments-processor2",      // NOT a match: no "-" boundary
		"analytics-a",              // unrelated transactional producer
		"audit",
	}
	got := correlateByStreamsConvention("payments-processor", txnIDs)
	want := []string{
		"payments-processor",
		"payments-processor-0_0",
		"payments-processor-abc12",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("correlateByStreamsConvention = %v, want %v", got, want)
	}

	// A group with no matching transactional id yields nothing (e.g. a plain
	// consumer group whose app is not transactional).
	if got := correlateByStreamsConvention("some-other-group", txnIDs); len(got) != 0 {
		t.Errorf("want no matches for unrelated group, got %v", got)
	}
}

func TestConsumedTopics_DropsInternal(t *testing.T) {
	offs := kadm.OffsetResponses{
		"payments.requests":  {0: {}, 1: {}},
		"__consumer_offsets": {12: {}}, // must be dropped
	}
	got := consumedTopics(offs)
	want := []string{"payments.requests"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("consumedTopics = %v, want %v", got, want)
	}
}

// TestEnrichmentMergesIntoProducedFootprint proves the pipeline contract: a
// consumer-group observation carrying the CONSUMED input topic, keyed by the same
// transactional id the admin/tail sources used for the PRODUCED topics, unions into
// one footprint. Grouping (see grouping package tests) then merges them into a single
// migration group and drops the internal topic — closing the read-process-write gap.
func TestEnrichmentMergesIntoProducedFootprint(t *testing.T) {
	acc := NewAccumulator()
	now := time.Now()
	const txnID = "payments-processor-abc12"

	// What the __transaction_state reader sees: only the produced topics
	// (plus __consumer_offsets, which marks the app read-process-write).
	acc.Add(Observation{
		TxnID:            txnID,
		Topics:           []string{"payments.approved", "payments.ledger", "__consumer_offsets"},
		ReadProcessWrite: true,
		Source:           SourceTxnStateLog,
		ObservedAt:       now,
	})
	// What Phase 3 enrichment adds: the consumed input topic, same txn id.
	acc.Add(Observation{
		TxnID:            txnID,
		Topics:           []string{"payments.requests"},
		ReadProcessWrite: true,
		Source:           SourceConsumerGroups,
		ObservedAt:       now,
	})

	snap := acc.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 footprint, got %d", len(snap))
	}
	got := snap[0].Topics
	want := []string{
		"__consumer_offsets",
		"payments.approved",
		"payments.ledger",
		"payments.requests",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged footprint = %v, want %v (consumed input must union with produced)", got, want)
	}
}
