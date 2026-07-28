package discovery

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTxnCatalog_ZeroProducerIDRegistersTxnIDWithoutAProducerMapping(t *testing.T) {
	// An early Empty __transaction_state record carries a transactional id but no real
	// producer id yet. The id must still be registered so the naming-based enrichment
	// phase can correlate it, but a 0 must never enter the producer-id map — every
	// unmapped producer would otherwise resolve to whichever transaction registered
	// first.
	c := NewTxnCatalog()

	c.Observe("tx-a", 0)

	assert.Equal(t, []string{"tx-a"}, c.TxnIDs())
	assert.Empty(t, c.ProducerIDToTxnID())
}

func TestTxnCatalog_PositiveProducerIDMapsToItsTransactionalID(t *testing.T) {
	// The producer-id mapping is what lets the __consumer_offsets phase join a
	// transactional offset commit back to its transaction without relying on a naming
	// convention.
	c := NewTxnCatalog()

	c.Observe("tx-a", 0)   // early record, no real producer id yet
	c.Observe("tx-a", 100) // later record carries it
	c.Observe("tx-b", 200)

	assert.ElementsMatch(t, []string{"tx-a", "tx-b"}, c.TxnIDs())
	assert.Equal(t, map[int64]string{100: "tx-a", 200: "tx-b"}, c.ProducerIDToTxnID())
}

func TestTxnCatalog_EmptyTransactionalIDIsIgnoredEntirely(t *testing.T) {
	// Abuse case: a malformed or non-transactional record decoding to an empty id must
	// not register a keyless entry, and must not claim its producer id — a producer
	// mapped to "" would later resolve an offset commit onto a nameless transaction.
	c := NewTxnCatalog()

	c.Observe("", 300)

	assert.Empty(t, c.TxnIDs())
	assert.Empty(t, c.ProducerIDToTxnID())
}

func TestTxnCatalog_ProducerIDSnapshotIsADefensiveCopy(t *testing.T) {
	// The enrichment phases hold the returned map for the length of a resolve pass and
	// are free to write to it. Handing out the live map would let one phase corrupt the
	// index the reader is still populating — and with no lock held, race it.
	c := NewTxnCatalog()
	c.Observe("tx", 1)

	snap := c.ProducerIDToTxnID()
	snap[1] = "mutated"
	snap[999] = "injected"

	assert.Equal(t, map[int64]string{1: "tx"}, c.ProducerIDToTxnID())
}

func TestTxnCatalog_ObserveAndSnapshotAreSafeConcurrently(t *testing.T) {
	// The __transaction_state reader writes to the catalog from its fetch loop while the
	// enrichment phases snapshot it on their refresh cadence. Run under -race.
	const writers, perWriter = 8, 200
	c := NewTxnCatalog()

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				c.Observe(fmt.Sprintf("tx-%d", w), int64(w*perWriter+i+1))
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range writers * perWriter {
			_ = c.TxnIDs()
			_ = c.ProducerIDToTxnID()
		}
	}()
	wg.Wait()

	assert.Len(t, c.TxnIDs(), writers)
	assert.Len(t, c.ProducerIDToTxnID(), writers*perWriter)
}
