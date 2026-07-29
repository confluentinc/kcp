package discovery

import (
	"reflect"
	"sort"
	"testing"
)

func TestTxnCatalog_ObserveAndSnapshot(t *testing.T) {
	c := NewTxnCatalog()

	// A producer id of 0 registers the txn id but not a producer mapping.
	c.Observe("tx-a", 0)
	c.Observe("tx-a", 100) // later record carries the real producer id
	c.Observe("tx-b", 200)
	c.Observe("", 300) // empty txn id is ignored entirely

	ids := c.TxnIDs()
	sort.Strings(ids)
	if want := []string{"tx-a", "tx-b"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("TxnIDs = %v, want %v", ids, want)
	}

	pid := c.ProducerIDToTxnID()
	if want := map[int64]string{100: "tx-a", 200: "tx-b"}; !reflect.DeepEqual(pid, want) {
		t.Errorf("ProducerIDToTxnID = %v, want %v", pid, want)
	}
}

// TestTxnCatalog_SnapshotIsCopy verifies callers can mutate a returned snapshot
// without corrupting the catalog's internal state.
func TestTxnCatalog_SnapshotIsCopy(t *testing.T) {
	c := NewTxnCatalog()
	c.Observe("tx", 1)

	pid := c.ProducerIDToTxnID()
	pid[1] = "mutated"
	pid[999] = "injected"

	if got := c.ProducerIDToTxnID(); got[1] != "tx" || len(got) != 1 {
		t.Errorf("catalog state leaked through snapshot: %v", got)
	}
}
