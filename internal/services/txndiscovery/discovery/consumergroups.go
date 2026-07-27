package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/txndiscovery/grouping"
)

// ConsumerGroupAdmin is the slice of sarama.ClusterAdmin consumer-group enrichment
// needs. It is declared here, narrowly, rather than reached through kcp's KafkaAdmin
// interface: that interface exposes five unrelated methods and keeps its
// sarama.ClusterAdmin unexported, so widening it would ripple through every existing
// mock in the repo for the sake of two calls made by one command.
//
// *sarama.ClusterAdmin satisfies this as-is. The caller constructs one (over the same
// sarama.Client the tail holds) and owns closing it; the enricher never does either.
type ConsumerGroupAdmin interface {
	// ListConsumerGroups returns group id -> protocol type for every group the
	// credentials can describe.
	ListConsumerGroups() (map[string]string, error)

	// ListConsumerGroupOffsets returns a group's committed offsets. A nil
	// topicPartitions asks for every topic the group has committed to.
	ListConsumerGroupOffsets(group string, topicPartitions map[string][]int32) (*sarama.OffsetFetchResponse, error)
}

// ConsumerGroupEnricher recovers the CONSUMED (input) topics of read-process-write /
// EOS apps, which are invisible in a transaction's footprint: __transaction_state
// reports only the topics a transaction PRODUCED to (plus __consumer_offsets), never
// the topics it consumed FROM. Without this, a consume-transform-produce app looks
// like it touches only its output topics, so its inputs could be left behind on the
// source cluster at cutover — breaking exactly-once.
//
// The topics a group has committed offsets for are exactly the topics it consumes, so
// the recovery is a consumer-group listing joined to the transactional ids the
// __transaction_state reader has already put in the shared TxnCatalog — no
// ListTransactions call.
//
// The join is the Kafka Streams naming convention (see correlateByStreamsConvention),
// which covers Kafka Streams apps — the dominant EOS workload. A plain
// consumer+producer EOS app whose transactional.id bears no relation to its group.id
// is handled instead by the __consumer_offsets phase's exact producer-id correlation.
type ConsumerGroupEnricher struct {
	Admin    ConsumerGroupAdmin
	Catalog  *TxnCatalog
	Interval time.Duration
	Log      *slog.Logger
}

// Name returns the source name the observations this phase emits are tagged with.
func (e *ConsumerGroupEnricher) Name() string { return SourceConsumerGroups }

// Run enriches on entry and then on every Interval tick until ctx is done. The first
// pass is immediate so an observation window shorter than the interval still enriches.
func (e *ConsumerGroupEnricher) Run(ctx context.Context, out chan<- Observation) error {
	e.enrichLogErr(ctx, out)
	ticker := time.NewTicker(e.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			e.enrichLogErr(ctx, out)
		}
	}
}

// enrichLogErr runs one pass, logging rather than propagating its error so a single
// failed pass — a coordinator move, a rebalance, a momentary ACL problem — never
// aborts the observation window. The cancellation check keeps shutdown from being
// reported as a failure.
func (e *ConsumerGroupEnricher) enrichLogErr(ctx context.Context, out chan<- Observation) {
	if err := e.enrich(ctx, out); err != nil && ctx.Err() == nil {
		e.Log.Warn("❌ consumer-group enrichment pass failed", "source", e.Name(), "err", err)
	}
}

// enrich runs one pass: list the groups, correlate each to the catalog, and emit the
// consumed topics of every group that correlates.
func (e *ConsumerGroupEnricher) enrich(ctx context.Context, out chan<- Observation) error {
	// The transactional ids come from the __transaction_state reader via the shared
	// catalog, not a ListTransactions call. Early in the run it may still be empty, and
	// on an idle cluster it stays empty — with nothing to correlate against a pass can
	// only produce nothing, so it costs the cluster no admin calls at all.
	txnIDs := e.Catalog.TxnIDs()
	if len(txnIDs) == 0 {
		return nil
	}

	groups, err := e.Admin.ListConsumerGroups()
	if err != nil {
		return fmt.Errorf("list consumer groups: %w", err)
	}

	now := time.Now()
	for group := range groups {
		matches := correlateByStreamsConvention(group, txnIDs)
		if len(matches) == 0 {
			continue
		}
		resp, ferr := e.Admin.ListConsumerGroupOffsets(group, nil)
		if ferr != nil {
			// Skip this group, not the pass: a single application rebalancing must not
			// decide whether every other application on the cluster gets enriched. The
			// group id is deliberately absent from the line — it names a customer's
			// application, and kcp.log is unconditionally Debug+.
			e.Log.Warn("❌ failed to fetch consumer-group offsets", "source", e.Name(), "err", ferr)
			continue
		}
		consumed := consumedTopics(resp)
		if len(consumed) == 0 {
			continue
		}
		for _, txnID := range matches {
			obs := Observation{
				TxnID: txnID,
				// No ProducerID: this phase correlates on names, not producer ids. The
				// accumulator only overwrites on a positive value, so leaving it zero
				// cannot erase the real id the __transaction_state reader recorded.
				Topics: consumed,
				// A group correlated to a transaction is a consume-transform-produce app
				// by construction, and these are its inputs.
				ReadProcessWrite: true,
				Source:           e.Name(),
				ObservedAt:       now,
			}
			select {
			case out <- obs:
			case <-ctx.Done():
				return nil
			}
		}
	}
	return nil
}

// consumedTopics returns the topics a group has committed offsets for — i.e. the
// topics it consumes — named once each, sorted.
//
// It takes sarama's response rather than a client-specific offset type so the helper
// stays a pure function over the wire shape.
func consumedTopics(resp *sarama.OffsetFetchResponse) []string {
	set := make(map[string]struct{}, len(resp.Blocks))
	for topic := range resp.Blocks {
		// An EOS group commits its offsets through the transaction, so
		// __consumer_offsets appears among its committed topics. Grouping drops internal
		// topics too, but reporting one here would credit it as a recovered INPUT.
		if grouping.IsInternalTopic(topic) {
			continue
		}
		set[topic] = struct{}{}
	}
	return sortedKeys(set)
}

// correlateByStreamsConvention returns the transactional ids that belong to the
// consumer group under the Kafka Streams naming rule: transactional.id is either
// "<group>" or "<group>-<suffix>". Kafka Streams sets application.id == group.id and
// derives transactional.id as "<application.id>-<processId|taskId>".
//
// The separator is part of the prefix on purpose. Matching on the bare group name
// would correlate "payments-processor2" — a different application — to the
// "payments-processor" group, folding one workload's input topics into another
// workload's transaction.
func correlateByStreamsConvention(group string, txnIDs []string) []string {
	// An empty group id is reachable (pre-2.2 simple consumers committed under "") and
	// degenerates the boundary prefix to "-", which matches every transactional id that
	// merely starts with a hyphen. It carries no application name, so it correlates to
	// nothing.
	if group == "" {
		return nil
	}
	prefix := group + "-"
	var out []string
	for _, id := range txnIDs {
		if id == group || strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	return out
}
