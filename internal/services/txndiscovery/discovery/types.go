// Package discovery observes transactions on a running cluster and accumulates the
// set of topics each transaction touches.
//
// The design separates observation (sources, which emit raw Observations) from
// accumulation (Accumulator) from grouping (the grouping package). Each source is
// independent, so adding one needs no change to the accumulator or the grouping stage.
package discovery

import "time"

// Source name constants, used for provenance in Observations and in the audit trail.
const (
	SourceTxnStateLog     = "transaction-state-log"
	SourceConsumerGroups  = "consumer-groups"
	SourceConsumerOffsets = "consumer-offsets-log"
)

// Observation is a single sighting of one transaction's topic footprint.
type Observation struct {
	TxnID string

	// ProducerID is the producer id the source saw for this transaction, or 0 when the
	// source has none — the naming-based enrichment phase never has one.
	ProducerID int64

	// Topics is the raw footprint reported by the source, INCLUDING Kafka-internal
	// topics such as __consumer_offsets. Filtering is deferred to the grouping stage so
	// that sources stay simple and their output stays auditable.
	Topics []string

	// ReadProcessWrite is set when the raw footprint included __consumer_offsets, i.e.
	// the transaction committed consumer offsets (a consume-transform-produce app).
	// Such an app's CONSUMED input topics are NOT in the transaction footprint; the
	// enrichment phases recover them and set this flag on the observations they emit.
	ReadProcessWrite bool

	// Source names the phase that made the sighting — one of the Source* constants.
	// It is the provenance an audit line records, so an operator reading the trace can
	// tell which phase coupled two topics.
	Source string

	// ObservedAt is when the source made the sighting.
	ObservedAt time.Time
}
