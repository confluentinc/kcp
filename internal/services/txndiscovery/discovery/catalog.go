package discovery

import "sync"

// TxnCatalog is the shared index the __transaction_state reader populates and the
// enrichment phases read. Both facts the enrichment phases need to correlate on — the
// set of live transactional ids, and each transaction's producer id — are present on
// every state record the reader decodes, so the catalog lets those phases correlate
// without calling the transaction admin APIs at all.
//
// Safe for concurrent use: the reader writes to it from its fetch loop while the
// enrichment phases snapshot it on their refresh cadence.
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

// Observe records one sighting of a transactional id and, if present, its producer
// id, as decoded from a __transaction_state record. A producerID of 0 or less is
// ignored for the producer-id mapping — an early Empty record may carry no real id
// yet — but the transactional id is always registered so the naming-based enrichment
// phase can still correlate it.
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

// TxnIDs returns a snapshot of every transactional id observed so far.
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
func (c *TxnCatalog) ProducerIDToTxnID() map[int64]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Defensive copy: callers hold this map across a resolve pass, so handing out the
	// live one would let a reader's write race an enrichment phase's read.
	out := make(map[int64]string, len(c.pidToTxn))
	for pid, id := range c.pidToTxn {
		out[pid] = id
	}
	return out
}
