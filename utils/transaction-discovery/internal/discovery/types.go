// Package discovery observes transactions on a running cluster and accumulates the
// set of topics each transaction touches.
//
// The design separates observation (Sources, which emit raw Observations) from
// accumulation (Accumulator) from grouping (the grouping package). Three Sources feed
// the pipeline: the TxnStateLogTail (the source of truth — reads __transaction_state
// from the start and reconstructs each transaction's footprint), the
// ConsumerGroupEnricher (Phase 3, recovers consumed input topics via the Kafka Streams
// naming convention), and the ConsumerOffsetsTail (Phase 4, recovers consumed input
// topics via EXACT producer-id correlation through __consumer_offsets — covering
// arbitrary non-Streams EOS apps). The latter two read the transactional ids and
// producer ids they correlate on from the shared TxnCatalog the reader populates, so
// no source calls the transaction admin APIs. Each is independent, so adding one needs
// no changes to the accumulator or grouping stages.
package discovery

import (
	"context"
	"time"
)

// Source name constants, used for provenance in Observations.
const (
	SourceTxnStateLog     = "transaction-state-log"
	SourceConsumerGroups  = "consumer-groups"
	SourceConsumerOffsets = "consumer-offsets-log"
)

// Observation is a single sighting of one transaction's topic footprint.
type Observation struct {
	TxnID      string
	ProducerID int64

	// Topics is the raw footprint reported by the source, INCLUDING Kafka-internal
	// topics such as __consumer_offsets. Filtering is deferred to the grouping stage
	// so that sources stay simple and their output stays auditable.
	Topics []string

	// ReadProcessWrite is set when the raw footprint included __consumer_offsets,
	// i.e. the transaction committed consumer offsets (a consume-transform-produce /
	// EOS app). Such an app's CONSUMED input topics are NOT present in the transaction
	// footprint; Phase 3 (ConsumerGroupEnricher, Streams naming) and Phase 4
	// (ConsumerOffsetsTail, exact producer-id correlation) recover them. This flag
	// drives both the recovery and the report wording (see the README). Phase 3/4
	// themselves set it on the recovered-input observations they emit.
	ReadProcessWrite bool

	Source     string
	ObservedAt time.Time
}

// Source emits Observations on out until ctx is cancelled, then returns.
type Source interface {
	Name() string
	Run(ctx context.Context, out chan<- Observation) error
}
