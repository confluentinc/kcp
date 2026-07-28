package offset

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"golang.org/x/sync/errgroup"
)

// retryBackoff is how long GetMany waits after refreshing metadata before
// retrying failed partitions. An immediate retry tends to observe the same
// in-flight leader election that failed the first pass; a short pause gives
// the cluster time to settle. A var (not const) so tests exercising the
// retry path can shrink it.
var retryBackoff = 500 * time.Millisecond

// Provider abstracts offset retrieval for testability.
type Provider interface {
	GetMany(ctx context.Context, topics []string) (map[string]map[int32]int64, error)
}

// Service provides offset operations against a Kafka cluster.
type Service struct {
	client sarama.Client
}

// NewOffsetService creates a Service backed by the given Kafka client.
func NewOffsetService(client sarama.Client) *Service {
	return &Service{client: client}
}

// Close closes the underlying Kafka client.
func (t *Service) Close() error {
	return t.client.Close()
}

// Get fetches the log end offset (LEO) for every partition of a topic. It
// is a single-topic wrapper over GetMany kept for the package tests and the
// offsetbench loop-vs-batch contrast; production sweeps call GetMany, which
// costs one request per broker instead of one per topic.
func (t *Service) Get(ctx context.Context, topic string) (map[int32]int64, error) {
	results, err := t.GetMany(ctx, []string{topic})
	if err != nil {
		return nil, err
	}
	return results[topic], nil
}

// topicPartition identifies a single partition during a batched fetch.
type topicPartition struct {
	topic     string
	partition int32
}

// failedPartition pairs a partition with the error that deferred it to the
// retry pass, so the sweep's final error can name the underlying cause.
type failedPartition struct {
	tp    topicPartition
	cause error
}

// GetMany fetches the log end offset (LEO) for every partition of every
// topic in a single sweep. Partitions are grouped by leader broker across
// all topics and one ListOffsets request is sent per broker (concurrently),
// so a sweep of N topics costs one round trip instead of N.
//
// Partitions that fail because cached metadata went stale mid-sweep (leader
// moved, replica unavailable, or the cached leader broker unreachable) are
// retried once against refreshed metadata before the sweep fails.
func (t *Service) GetMany(ctx context.Context, topics []string) (map[string]map[int32]int64, error) {
	results := make(map[string]map[int32]int64, len(topics))
	if len(topics) == 0 {
		return results, nil
	}

	slog.Debug("fetching offsets for topic batch", "topics", len(topics))

	pending := make([]topicPartition, 0, len(topics))
	for _, topic := range topics {
		partitions, err := t.client.Partitions(topic)
		if err != nil {
			return nil, fmt.Errorf("failed to get partitions for topic %q: %w", topic, err)
		}
		results[topic] = make(map[int32]int64, len(partitions))
		for _, p := range partitions {
			pending = append(pending, topicPartition{topic: topic, partition: p})
		}
	}

	retriable, err := t.fetchBatch(pending, results)
	if err != nil {
		return nil, err
	}
	if len(retriable) > 0 {
		retry := make([]topicPartition, 0, len(retriable))
		refresh := make([]string, 0, len(retriable))
		seen := make(map[string]struct{}, len(retriable))
		for _, f := range retriable {
			retry = append(retry, f.tp)
			if _, dup := seen[f.tp.topic]; !dup {
				seen[f.tp.topic] = struct{}{}
				refresh = append(refresh, f.tp.topic)
			}
		}
		slog.Debug("retrying offset fetch after metadata refresh",
			"partitions", len(retriable), "topics", len(refresh))
		if err := t.client.RefreshMetadata(refresh...); err != nil {
			return nil, fmt.Errorf("failed to refresh metadata for retry: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryBackoff):
		}

		stillFailed, err := t.fetchBatch(retry, results)
		if err != nil {
			return nil, err
		}
		if len(stillFailed) > 0 {
			return nil, fmt.Errorf("offset fetch failed for %d partition(s) after metadata refresh: %w",
				len(stillFailed), stillFailed[0].cause)
		}
	}

	slog.Debug("fetched offsets for topic batch", "topics", len(topics))
	return results, nil
}

// fetchBatch groups partitions by leader broker, issues one ListOffsets
// request per broker, and writes offsets into results. Partitions whose
// leader lookup failed, whose offset block carried a stale-metadata error,
// or whose broker request failed at the transport level are returned (with
// their cause) for the caller to retry; any other failure aborts the batch.
func (t *Service) fetchBatch(tps []topicPartition, results map[string]map[int32]int64) ([]failedPartition, error) {
	var failed []failedPartition

	brokerPartitions := make(map[*sarama.Broker][]topicPartition)
	for _, tp := range tps {
		leader, err := t.client.Leader(tp.topic, tp.partition)
		if err != nil {
			// Leader lookup fails when cached metadata is stale; defer to
			// the caller's refresh-and-retry pass.
			failed = append(failed, failedPartition{
				tp:    tp,
				cause: fmt.Errorf("failed to get leader for %s/%d: %w", tp.topic, tp.partition, err),
			})
			continue
		}
		brokerPartitions[leader] = append(brokerPartitions[leader], tp)
	}

	// Brokers are independent, so fan the per-broker requests out
	// concurrently: the sweep costs one round trip, not brokers' worth.
	var (
		mu sync.Mutex
		g  errgroup.Group
	)
	for broker, parts := range brokerPartitions {
		g.Go(func() error {
			req := &sarama.OffsetRequest{
				Version: 1,
			}
			for _, tp := range parts {
				req.AddBlock(tp.topic, tp.partition, sarama.OffsetNewest, 1)
			}

			resp, err := broker.GetAvailableOffsets(req)
			if err != nil {
				// A transport-level failure (connection refused/reset, EOF from
				// a restarting broker) is the blunt form of stale metadata: the
				// cached leader is gone. Defer every partition routed to this
				// broker to the refresh-and-retry pass — after the refresh they
				// re-route to the current leaders — rather than failing the
				// sweep on a condition the retry exists to absorb.
				//
				// Deliberately unclassified: persistent transport failures
				// (bad TLS/SASL config, dead cluster) also take this path and
				// cost one refresh + backoff + retry per sweep before
				// surfacing. That bounded delay is accepted because reliably
				// separating "broker restarting" from "misconfigured" out of
				// the wrapped error is not feasible, and misclassifying a
				// transient as fatal would abort a live migration.
				mu.Lock()
				for _, tp := range parts {
					failed = append(failed, failedPartition{
						tp:    tp,
						cause: fmt.Errorf("offset request to broker %s failed for %s/%d: %w", broker.Addr(), tp.topic, tp.partition, err),
					})
				}
				mu.Unlock()
				return nil
			}

			mu.Lock()
			defer mu.Unlock()
			for _, tp := range parts {
				block := resp.GetBlock(tp.topic, tp.partition)
				switch {
				case block == nil:
					return fmt.Errorf("no offset block returned for %s/%d", tp.topic, tp.partition)
				case block.Err == sarama.ErrNoError:
					results[tp.topic][tp.partition] = block.Offset
				case isStaleMetadataErr(block.Err):
					failed = append(failed, failedPartition{
						tp:    tp,
						cause: fmt.Errorf("offset error for %s/%d: %w", tp.topic, tp.partition, block.Err),
					})
				default:
					return fmt.Errorf("offset error for %s/%d: %v", tp.topic, tp.partition, block.Err)
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return failed, nil
}

// isStaleMetadataErr reports whether a per-partition error indicates the
// client's cached metadata is out of date (so a refresh-and-retry can
// succeed) rather than a persistent failure. UNKNOWN_TOPIC_OR_PARTITION is
// included because a broker that no longer hosts a partition (leadership
// moved since the metadata was cached) answers with it; for a genuinely
// deleted topic the retry fails and the specific error is surfaced via the
// failedPartition cause.
func isStaleMetadataErr(err sarama.KError) bool {
	switch err {
	case sarama.ErrNotLeaderForPartition,
		sarama.ErrLeaderNotAvailable,
		sarama.ErrUnknownTopicOrPartition,
		sarama.ErrReplicaNotAvailable:
		return true
	}
	return false
}

// Exists checks whether a topic exists on the cluster by refreshing metadata.
func (t *Service) Exists(topic string) (bool, error) {
	if err := t.client.RefreshMetadata(); err != nil {
		return false, fmt.Errorf("failed to refresh metadata: %w", err)
	}
	topics, err := t.client.Topics()
	if err != nil {
		return false, fmt.Errorf("failed to list topics: %w", err)
	}

	if slices.Contains(topics, topic) {
		return true, nil
	}
	return false, nil
}

// SortedPartitionIDs returns the union of partition IDs from two offset maps, sorted ascending.
func SortedPartitionIDs(src, dst map[int32]int64) []int32 {
	seen := make(map[int32]struct{})
	for p := range src {
		seen[p] = struct{}{}
	}
	for p := range dst {
		seen[p] = struct{}{}
	}

	ids := make([]int32, 0, len(seen))
	for p := range seen {
		ids = append(ids, p)
	}
	slices.Sort(ids)
	return ids
}

// ComputeTotalLag computes the total offset lag across all partitions.
// For partitions missing from dst, the full source offset counts as lag.
func ComputeTotalLag(src, dst map[int32]int64) int64 {
	var total int64
	for p, srcOffset := range src {
		dstOffset, ok := dst[p]
		if !ok {
			total += srcOffset
			continue
		}
		lag := srcOffset - dstOffset
		if lag > 0 {
			total += lag
		}
	}
	return total
}
