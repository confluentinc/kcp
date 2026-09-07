package discovery

import (
	"reflect"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kmsg"
)

// TestDecodeOffsetCommitKey round-trips the on-disk __consumer_offsets key formats
// through the same kmsg types the decoder routes to: versions 0/1 are offset commits
// carrying the consumed topic; version 2 is group metadata (no topic); junk is rejected.
func TestDecodeOffsetCommitKey(t *testing.T) {
	offsetKeyV0 := (&kmsg.OffsetCommitKey{Version: 0, Group: "grp-a", Topic: "orders.inbound", Partition: 3}).AppendTo(nil)
	offsetKeyV1 := (&kmsg.OffsetCommitKey{Version: 1, Group: "grp-b", Topic: "payments.requests", Partition: 0}).AppendTo(nil)
	groupMetaV2 := (&kmsg.GroupMetadataKey{Version: 2, Group: "grp-a"}).AppendTo(nil)

	tests := []struct {
		name      string
		key       []byte
		wantGroup string
		wantTopic string
		wantOK    bool
	}{
		{"offset commit v0", offsetKeyV0, "grp-a", "orders.inbound", true},
		{"offset commit v1", offsetKeyV1, "grp-b", "payments.requests", true},
		{"group metadata v2 (no topic)", groupMetaV2, "grp-a", "", true},
		{"too short", []byte{0x00}, "", "", false},
		{"unknown version", []byte{0x00, 0x63, 0x00}, "", "", false}, // version 99
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			group, topic, ok := decodeOffsetCommitKey(tc.key)
			if ok != tc.wantOK || group != tc.wantGroup || topic != tc.wantTopic {
				t.Errorf("decodeOffsetCommitKey = (%q, %q, %v), want (%q, %q, %v)",
					group, topic, ok, tc.wantGroup, tc.wantTopic, tc.wantOK)
			}
		})
	}
}

// TestExactProducerIdCorrelation_IgnoresNames is the crux of Phase 4: a consumed input
// is tied to its transaction purely by producer id, so it works even when the consumer
// group name bears NO relation to the transactional.id — exactly the non-Streams case
// Phase 3's naming heuristic cannot handle. The test asserts both halves: Phase 3 would
// NOT link these names, but Phase 4 does.
func TestExactProducerIdCorrelation_IgnoresNames(t *testing.T) {
	const (
		group = "orders-consumer-grp" // deliberately unrelated to the txn id below
		txnID = "orders-writer-tx"
		pid   = int64(42)
		input = "orders.inbound"
	)

	// Sanity: the Streams naming heuristic (Phase 3) cannot correlate these.
	if got := correlateByStreamsConvention(group, []string{txnID}); len(got) != 0 {
		t.Fatalf("precondition failed: names should NOT match by Streams convention, got %v", got)
	}

	tail := &ConsumerOffsetsTail{}
	tail.recordCommit(pid, group, input)

	now := time.Now()
	got := tail.resolveWith(map[int64]string{pid: txnID}, now)
	want := []Observation{{
		TxnID:            txnID,
		ProducerID:       pid,
		Topics:           []string{input},
		ReadProcessWrite: true,
		Source:           SourceConsumerOffsets,
		ObservedAt:       now,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveWith = %+v, want %+v", got, want)
	}

	// Stats reflect the exact link.
	st := tail.Stats()
	if st.GroupsLinked != 1 || st.Correlations != 1 {
		t.Errorf("stats groupsLinked=%d correlations=%d, want 1/1", st.GroupsLinked, st.Correlations)
	}
	if !reflect.DeepEqual(st.RecoveredTopics, []string{input}) {
		t.Errorf("recovered topics = %v, want [%q]", st.RecoveredTopics, input)
	}
}

// TestResolveWith_UnresolvedStaysPending verifies a sighting whose producer id is not
// (yet) listable is retained and resolved on a later pass, not dropped — the buffering
// that makes the tail robust to seeing an offset commit before its transaction lists.
func TestResolveWith_UnresolvedStaysPending(t *testing.T) {
	tail := &ConsumerOffsetsTail{}
	tail.recordCommit(7, "g", "topic.in")

	if got := tail.resolveWith(map[int64]string{}, time.Now()); len(got) != 0 {
		t.Fatalf("want nothing emitted while producer id is unlistable, got %v", got)
	}
	// Now the transaction becomes listable.
	got := tail.resolveWith(map[int64]string{7: "tx-1"}, time.Now())
	if len(got) != 1 || got[0].TxnID != "tx-1" || got[0].Topics[0] != "topic.in" {
		t.Fatalf("want the buffered sighting resolved to tx-1, got %+v", got)
	}
	// Once resolved it is removed, so a repeat pass emits nothing.
	if again := tail.resolveWith(map[int64]string{7: "tx-1"}, time.Now()); len(again) != 0 {
		t.Errorf("resolved sighting should not re-emit, got %v", again)
	}
}

// TestResolveWith_MergesTopicsPerProducer verifies multiple consumed topics committed
// by the same producer are gathered into one observation (sorted, deduped).
func TestResolveWith_MergesTopicsPerProducer(t *testing.T) {
	tail := &ConsumerOffsetsTail{}
	tail.recordCommit(9, "g", "beta")
	tail.recordCommit(9, "g", "alpha")
	tail.recordCommit(9, "g", "alpha") // dup

	got := tail.resolveWith(map[int64]string{9: "tx"}, time.Now())
	if len(got) != 1 {
		t.Fatalf("want one observation, got %d", len(got))
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(got[0].Topics, want) {
		t.Errorf("topics = %v, want %v (sorted, deduped)", got[0].Topics, want)
	}
}

// TestExactCorrelationFoldsInputIntoProducedGroup is the pipeline contract for Phase 4,
// mirroring the Phase 3 test: a Phase 4 observation carrying the CONSUMED input under
// the same transactional id the write-side sources used for the PRODUCED topics unions
// into one footprint — so grouping folds the input into the produced group.
func TestExactCorrelationFoldsInputIntoProducedGroup(t *testing.T) {
	acc := NewAccumulator()
	now := time.Now()
	const txnID = "orders-writer-tx" // no relation to the consumer group name

	// Write side (the __transaction_state reader): produced topics + __consumer_offsets.
	acc.Add(Observation{
		TxnID:            txnID,
		ProducerID:       42,
		Topics:           []string{"orders.processed", "__consumer_offsets"},
		ReadProcessWrite: true,
		Source:           SourceTxnStateLog,
		ObservedAt:       now,
	})
	// Phase 4: the consumed input, correlated by producer id, under the same txn id.
	acc.Add(Observation{
		TxnID:            txnID,
		ProducerID:       42,
		Topics:           []string{"orders.inbound"},
		ReadProcessWrite: true,
		Source:           SourceConsumerOffsets,
		ObservedAt:       now,
	})

	snap := acc.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 footprint, got %d", len(snap))
	}
	want := []string{"__consumer_offsets", "orders.inbound", "orders.processed"}
	if got := snap[0].Topics; !reflect.DeepEqual(got, want) {
		t.Errorf("merged footprint = %v, want %v (consumed input must union with produced)", got, want)
	}
	if snap[0].ProducerID != 42 {
		t.Errorf("producer id = %d, want 42 (must survive the zero-id Phase 3 guard)", snap[0].ProducerID)
	}
}
