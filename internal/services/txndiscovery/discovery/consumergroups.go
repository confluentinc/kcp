package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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

	mu           sync.Mutex
	passes       int
	passFailures int
	groupsListed int
	// correlations holds the distinct "group\x00txnID" links made over the run. The
	// listing repeats every pass, so a counter would report the same recovery once per
	// pass; the set is what makes the number the run's total.
	correlations map[string]struct{}
}

// EnricherStats is what this phase contributes to the run's report.
type EnricherStats struct {
	// Passes is how many enrichment passes completed.
	//
	// It is the cadence signal, and the reason this struct exists: this phase's admin
	// calls are the run's slowest, so an --interval that should yield thirteen passes
	// over a window can yield four, and nothing else in the report would say so.
	//
	// Every completed pass counts, including one that short-circuited on an empty
	// catalog. The question is how many times the loop came round; a run against an
	// idle cluster reporting zero passes would read as a phase that never started.
	Passes int

	// PassFailures is how many passes failed at the group listing.
	//
	// Kept apart from Passes because a phase whose every pass failed would otherwise
	// report a full-cadence run — thirteen passes, no correlations — which is
	// indistinguishable from a healthy run against a cluster with no exactly-once
	// traffic. A single failed pass never aborts the window, so this count is the only
	// artifact that says the phase was degraded.
	PassFailures int

	// GroupsListed is the most consumer groups any one pass listed.
	//
	// It separates "the naming convention matched nothing on this cluster" from "the
	// credentials could not see a single group". Both recover no inputs, and Passes
	// alone reports them identically.
	//
	// The most any pass saw rather than a running total, because the listing repeats
	// every pass: a sum would multiply one cluster's group count by the cadence, and
	// the last pass's figure would let a rebalance at the window's close understate the
	// estate.
	GroupsListed int

	// Correlations is how many distinct consumer-group-to-transaction links this phase
	// made over the run.
	//
	// Counted when the link is MADE, not when its observation is delivered, and the
	// difference is the point: a correlation counted here that the report's naming
	// attribution (Recovery.ByNamingTxns) does not credit was computed and then
	// discarded at the send — R8's recovery silently not happening, which is what the
	// final pass at the window's close exists to prevent.
	Correlations int
}

// Stats returns a snapshot of what this phase has done so far.
func (e *ConsumerGroupEnricher) Stats() EnricherStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EnricherStats{
		Passes:       e.passes,
		PassFailures: e.passFailures,
		GroupsListed: e.groupsListed,
		Correlations: len(e.correlations),
	}
}

// recordListing notes how many groups one pass saw, keeping the largest.
func (e *ConsumerGroupEnricher) recordListing(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n > e.groupsListed {
		e.groupsListed = n
	}
}

// recordCorrelation notes one group-to-transaction link, deduplicated across passes.
func (e *ConsumerGroupEnricher) recordCorrelation(group, txnID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.correlations == nil {
		e.correlations = make(map[string]struct{})
	}
	// NUL separator: it cannot occur in a group id or a transactional id, so two
	// different links cannot collide into one key.
	e.correlations[group+"\x00"+txnID] = struct{}{}
}

// countPass records that one pass finished its work.
func (e *ConsumerGroupEnricher) countPass() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.passes++
}

// countPassFailure records that one pass did not get as far as finishing.
func (e *ConsumerGroupEnricher) countPassFailure() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.passFailures++
}

// Name returns the source name the observations this phase emits are tagged with.
func (e *ConsumerGroupEnricher) Name() string { return SourceConsumerGroups }

// Run enriches on entry, then on every Interval tick, and once more as the window
// closes. The first pass is immediate so an observation window shorter than the
// interval still enriches; the last one is FinalEnrich, for the ids that arrived too
// late for any tick to act on.
func (e *ConsumerGroupEnricher) Run(ctx context.Context, out chan<- Observation) error {
	e.enrichLogErr(ctx, out)
	ticker := time.NewTicker(e.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.FinalEnrich(out)
			return nil
		case <-ticker.C:
			e.enrichLogErr(ctx, out)
		}
	}
}

// FinalEnrich runs one last pass as the window closes, on a fresh context.
//
// It exists because this phase's last pass is the one most likely to matter, and the
// one least likely to survive. The transactional ids come from the catalog, which the
// __transaction_state reader fills as it works from the beginning of a 50-partition
// compacted log, so an id can arrive tens of seconds into the window — and this
// phase's admin calls share one sarama Broker connection with that reader's fetches,
// which makes a pass cost seconds rather than milliseconds. The deciding pass
// therefore tends to straddle the end of the window, where the observation context is
// already cancelled: it correlates the group correctly and then discards the result at
// the send. Without this the recovery is computed and thrown away, and the app's input
// topic is reported as individually migratable — the exactly-once break R8 exists to
// prevent.
//
// This mirrors ConsumerOffsetsTail.FinalFlush, which exists for the same reason on the
// same catalog. The timeout is what keeps an unresponsive broker from wedging
// shutdown, and it also bounds the send: by this point the accumulator is still
// draining, but nothing may block the run from writing its artifacts indefinitely.
func (e *ConsumerGroupEnricher) FinalEnrich(out chan<- Observation) {
	ctx, cancel := context.WithTimeout(context.Background(), finalFlushTimeout)
	defer cancel()
	e.enrichLogErr(ctx, out)
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
		e.countPass()
		return nil
	}

	groups, err := e.Admin.ListConsumerGroups()
	if err != nil {
		e.countPassFailure()
		return fmt.Errorf("list consumer groups: %w", err)
	}
	e.recordListing(len(groups))

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
			e.recordCorrelation(group, txnID)
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
				// Abandoned at shutdown rather than finished, so it is not counted:
				// FinalEnrich's pass, on a fresh context, is the one that completes.
				return nil
			}
		}
	}
	e.countPass()
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
