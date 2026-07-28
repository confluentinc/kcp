package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

// ConsumerGroupEnricher is the Phase 3 data source. It recovers the CONSUMED
// (input) topics of read-process-write / EOS apps, which are invisible in a
// transaction's footprint: the __transaction_state log reports only the topics a
// transaction PRODUCED to (plus __consumer_offsets), never the topics it consumed
// FROM. Without this, a consume-transform-produce app looks like it touches only its
// output topics, so its inputs could be left behind on the source cluster at cutover
// — breaking exactly-once.
//
// It recovers those inputs through the consumer-group admin APIs (ListGroups +
// FetchOffsets): the topics a group has committed offsets for are exactly the topics
// it consumes. The set of transactional ids to correlate against comes from the shared
// TxnCatalog the __transaction_state reader populates — no ListTransactions call.
//
// Correlating a consumer group back to a transaction uses the Kafka Streams
// convention that transactional.id is prefixed with application.id, which equals
// the consumer group.id (Streams EOS: transactional.id = "<application.id>-...").
// That covers Kafka Streams apps — the dominant EOS workload. A plain
// consumer+producer EOS app whose transactional.id bears no relation to its group.id
// is handled instead by Phase 4's exact producer-id correlation via __consumer_offsets.
type ConsumerGroupEnricher struct {
	Admin    *kadm.Client
	Catalog  *TxnCatalog
	Interval time.Duration
	Log      *slog.Logger

	mu           sync.Mutex
	passes       int
	groupsLinked map[string]struct{}
	correlations map[string]struct{} // "group\x00txnID"
	recovered    map[string]struct{}
}

// EnricherStats summarises what consumer-group enrichment recovered over the run.
type EnricherStats struct {
	Passes          int
	GroupsLinked    int      // consumer groups correlated to at least one transaction
	Correlations    int      // (consumer group -> transaction) links found
	RecoveredTopics []string // consumed input topics folded back into groups (sorted)
}

func (e *ConsumerGroupEnricher) Name() string { return SourceConsumerGroups }

// Stats returns a snapshot of what enrichment recovered so far.
func (e *ConsumerGroupEnricher) Stats() EnricherStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EnricherStats{
		Passes:          e.passes,
		GroupsLinked:    len(e.groupsLinked),
		Correlations:    len(e.correlations),
		RecoveredTopics: sortedKeys(e.recovered),
	}
}

func (e *ConsumerGroupEnricher) Run(ctx context.Context, out chan<- Observation) error {
	e.enrichLogErr(ctx, out) // run once immediately so a short window still enriches
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

// enrichLogErr runs one pass, logging (rather than propagating) transient errors so
// a single failed pass never aborts the observation window.
func (e *ConsumerGroupEnricher) enrichLogErr(ctx context.Context, out chan<- Observation) {
	if err := e.enrich(ctx, out); err != nil && ctx.Err() == nil {
		e.Log.Warn("consumer-group enrichment pass failed", "source", e.Name(), "err", err)
	}
}

func (e *ConsumerGroupEnricher) enrich(ctx context.Context, out chan<- Observation) error {
	// The transactional ids come from the __transaction_state reader (via the shared
	// catalog), not a ListTransactions call. Early in the run the catalog may still be
	// empty; later passes pick up whatever the reader has decoded by then.
	txnIDs := e.Catalog.TxnIDs()
	if len(txnIDs) == 0 {
		return nil
	}

	groups, err := e.Admin.ListGroups(ctx)
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}

	now := time.Now()
	for _, g := range groups.Groups() {
		matches := correlateByStreamsConvention(g, txnIDs)
		if len(matches) == 0 {
			continue
		}
		offs, ferr := e.Admin.FetchOffsets(ctx, g)
		if ferr != nil {
			e.Log.Warn("fetch consumer-group offsets failed", "group", g, "err", ferr)
			continue
		}
		consumed := consumedTopics(offs)
		if len(consumed) == 0 {
			continue
		}
		for _, txnID := range matches {
			obs := Observation{
				TxnID:            txnID,
				Topics:           consumed,
				ReadProcessWrite: true,
				Source:           e.Name(),
				ObservedAt:       now,
			}
			select {
			case out <- obs:
			case <-ctx.Done():
				return nil
			}
			e.recordLink(g, txnID, consumed)
		}
	}
	e.recordPass()
	return nil
}

// correlateByStreamsConvention returns the transactional ids that belong to the
// consumer group under the Kafka Streams naming rule: transactional.id is either
// "<group>" or "<group>-<suffix>". Kafka Streams sets application.id == group.id
// and derives transactional.id as "<application.id>-<processId|taskId>".
func correlateByStreamsConvention(group string, txnIDs []string) []string {
	prefix := group + "-"
	var out []string
	for _, id := range txnIDs {
		if id == group || strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	return out
}

// consumedTopics returns the non-internal topics a group has committed offsets for
// — i.e. the topics it consumes.
func consumedTopics(offs kadm.OffsetResponses) []string {
	set := make(map[string]struct{})
	for topic := range offs {
		if strings.HasPrefix(topic, "__") {
			continue // Kafka-internal (e.g. __consumer_offsets); grouping drops these too
		}
		set[topic] = struct{}{}
	}
	return sortedKeys(set)
}

func (e *ConsumerGroupEnricher) recordPass() {
	e.mu.Lock()
	e.passes++
	e.mu.Unlock()
}

func (e *ConsumerGroupEnricher) recordLink(group, txnID string, topics []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.groupsLinked == nil {
		e.groupsLinked = map[string]struct{}{}
		e.correlations = map[string]struct{}{}
		e.recovered = map[string]struct{}{}
	}
	e.groupsLinked[group] = struct{}{}
	e.correlations[group+"\x00"+txnID] = struct{}{}
	for _, t := range topics {
		e.recovered[t] = struct{}{}
	}
}
