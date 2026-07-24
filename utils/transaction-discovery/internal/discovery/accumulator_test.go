package discovery

import (
	"testing"
	"time"
)

func TestAccumulator_UnionsAcrossSamples(t *testing.T) {
	acc := NewAccumulator()
	t0 := time.Now()

	acc.Add(Observation{TxnID: "app-0", Topics: []string{"a", "b"}, Source: SourceTxnStateLog, ObservedAt: t0})
	acc.Add(Observation{
		TxnID: "app-0", Topics: []string{"b", "c", "__consumer_offsets"},
		ReadProcessWrite: true, Source: SourceTxnStateLog, ObservedAt: t0.Add(time.Second),
	})

	snap := acc.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 accumulated txn, got %d", len(snap))
	}
	fp := snap[0]
	if len(fp.Topics) != 4 { // a, b, c, __consumer_offsets (unioned, deduped)
		t.Errorf("want 4 unioned topics, got %v", fp.Topics)
	}
	if !fp.ReadProcessWrite {
		t.Errorf("read-process-write flag should stick once observed")
	}
	if fp.Samples != 2 {
		t.Errorf("want 2 samples, got %d", fp.Samples)
	}
}

func TestAccumulator_SeparateTxnsStaySeparate(t *testing.T) {
	acc := NewAccumulator()
	now := time.Now()
	acc.Add(Observation{TxnID: "x", Topics: []string{"a"}, ObservedAt: now})
	acc.Add(Observation{TxnID: "y", Topics: []string{"b"}, ObservedAt: now})

	if got := len(acc.Snapshot()); got != 2 {
		t.Fatalf("want 2 txns, got %d", got)
	}
}

// TestAccumulator_UnionsAcrossSources proves the pipeline contract that lets the
// consumer-group phases fold a consumed input into the write-side footprint: two
// different sources reporting the same transactional id union into one footprint,
// crediting both sources.
func TestAccumulator_UnionsAcrossSources(t *testing.T) {
	acc := NewAccumulator()
	now := time.Now()
	acc.Add(Observation{TxnID: "t", Topics: []string{"out"}, Source: SourceTxnStateLog, ObservedAt: now})
	acc.Add(Observation{TxnID: "t", Topics: []string{"in"}, Source: SourceConsumerGroups, ObservedAt: now})

	snap := acc.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 footprint, got %d", len(snap))
	}
	if got, want := snap[0].Topics, []string{"in", "out"}; !equalStrs(got, want) {
		t.Errorf("topics = %v, want %v", got, want)
	}
	if len(snap[0].Sources) != 2 {
		t.Errorf("want both sources credited, got %v", snap[0].Sources)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
