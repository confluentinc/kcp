package discovery

import "sync"

// TxnCatalog is the shared index the __transaction_state reader populates and the
// consumer-group phases read. Now that __transaction_state is the single source of
// truth, the two facts the discovery run used to fetch with a separate
// ListTransactions call — the set of live transactional ids, and each transaction's
// producer id — are already present on every state record the reader decodes. The
// catalog captures them so Phase 3 and Phase 4 can correlate without calling the
// transaction admin APIs at all.
//
// It is safe for concurrent use: the reader writes to it from its fetch loop while
// the enrichment phases snapshot it on their refresh cadence.
type TxnCatalog struct {
	mu       sync.RWMutex
	pidToTxn map[int64]string    // producer id -> transactional id (last writer wins)
	txnIDs   map[string]struct{} // every transactional id ever observed
}

// NewTxnCatalog returns an empty catalog ready for concurrent use.
func NewTxnCatalog() *TxnCatalog {
	return &TxnCatalog{
		pidToTxn: make(map[int64]string),
		txnIDs:   make(map[string]struct{}),
	}
}

// Observe records one sighting of a transactional id and (if present) its producer
// id, as decoded from a __transaction_state record. producerID <= 0 is ignored for
// the producer-id mapping — an early Empty record may carry no real id yet — but the
// transactional id is always registered so Phase 3 can still correlate it by name.
func (c *TxnCatalog) Observe(txnID string, producerID int64) {
	if txnID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.txnIDs[txnID] = struct{}{}
	if producerID > 0 {
		c.pidToTxn[producerID] = txnID
	}
}

// TxnIDs returns a snapshot of every transactional id observed so far. Phase 3 uses
// it in place of ListTransactions().TransactionalIDs().
func (c *TxnCatalog) TxnIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.txnIDs))
	for id := range c.txnIDs {
		out = append(out, id)
	}
	return out
}

// ProducerIDToTxnID returns a snapshot of the producer-id -> transactional-id map.
// Phase 4 uses it in place of the producer id ListTransactions reported per txn.
func (c *TxnCatalog) ProducerIDToTxnID() map[int64]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[int64]string, len(c.pidToTxn))
	for pid, id := range c.pidToTxn {
		out[pid] = id
	}
	return out
}
