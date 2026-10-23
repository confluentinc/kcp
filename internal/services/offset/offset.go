package offset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/retry"
	"github.com/confluentinc/kcp/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
)

// tracerName is the instrumentation scope of this package's spans.
const tracerName = "kcp/services/offset"

// Provider abstracts offset retrieval for testability.
type Provider interface {
	GetMany(ctx context.Context, topics []string) (map[string]map[int32]int64, error)
}

// Service provides offset operations against a Kafka cluster.
type Service struct {
	client  sarama.Client
	cluster string // "source" or "destination", for span attributes; "" when unset
}

// NewOffsetService creates a Service backed by the given Kafka client.
func NewOffsetService(client sarama.Client) *Service {
	return &Service{client: client}
}

// WithCluster labels the service's spans with which cluster it reads
// ("source" or "destination") and returns it.
func (t *Service) WithCluster(cluster string) *Service {
	t.cluster = cluster
	return t
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

// failedPartition pairs a partition with the error that left it unread, so the sweep's final error can name
// the cause. broker is the leader whose connection failed, or nil when the leader lookup failed or the leader
// answered with a stale-metadata error.
type failedPartition struct {
	tp     topicPartition
	cause  error
	broker *sarama.Broker
}

// unreadPartitions is a sweep that left partitions unread because a leader lookup, a leader's connection or a
// partition's offset block failed. It wraps the first cause; getMany puts first a cause a retry cannot fix.
type unreadPartitions struct {
	failed []failedPartition
}

func (e *unreadPartitions) Error() string {
	return fmt.Sprintf("offset fetch failed for %d partition(s): %v", len(e.failed), e.failed[0].cause)
}

func (e *unreadPartitions) Unwrap() error { return e.failed[0].cause }

// GetMany fetches the log end offset (LEO) for every partition of every topic in a single sweep. Partitions
// are grouped by leader broker across all topics and one ListOffsets request is sent per broker (concurrently),
// so a sweep of N topics costs one round trip instead of N.
//
// Partitions that fail because cached metadata went stale (leader moved, replica unavailable) or because the
// leader's connection failed (internal/retry's Retryable) are fetched again within the retry budget: before each
// retry the failed leaders' connections are closed and their topics' metadata refreshed. A sweep in which any
// partition failed some other way, such as a failed login or a TLS error, fails at once with that cause. The lag
// wait and promote also tolerate 2 failed sweeps in a row, so on a cluster that stays unreachable they fail after
// about three budgets.
func (t *Service) GetMany(ctx context.Context, topics []string) (_ map[string]map[int32]int64, err error) {
	if len(topics) == 0 {
		return map[string]map[int32]int64{}, nil
	}
	ctx, span := tracing.Start(ctx, tracerName, "offset.get_many",
		attribute.String("kcp.cluster", t.cluster), attribute.Int("kcp.topics", len(topics)))
	defer func() { tracing.End(span, err) }()
	return t.getMany(ctx, topics)
}

// getMany is GetMany's body, run inside its offset.get_many span.
func (t *Service) getMany(ctx context.Context, topics []string) (map[string]map[int32]int64, error) {
	results := make(map[string]map[int32]int64, len(topics))

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

	tracing.SetAttributes(ctx, attribute.Int("kcp.partitions", len(pending)))

	var dead []*sarama.Broker
	var refresh []string
	err := retry.Do(ctx, "kafka.list_offsets", func(ctx context.Context) error {
		failed, err := t.fetchBatch(ctx, pending, results)
		if err != nil {
			return err
		}
		if len(failed) == 0 {
			return nil
		}
		if i := slices.IndexFunc(failed, func(f failedPartition) bool { return !retryableCause(f.cause) }); i > 0 {
			failed[0], failed[i] = failed[i], failed[0] // the error names and wraps the cause that stops the sweep
		}
		pending, dead, refresh = retryPlan(failed)
		tracing.SetAttributes(ctx, attribute.Int("kcp.retried_partitions", len(pending)))
		return &unreadPartitions{failed: failed}
	},
		retry.WithRetryable(func(err error) bool {
			var unread *unreadPartitions
			if errors.As(err, &unread) {
				return retryableUnread(unread.failed)
			}
			return retry.Retryable(err)
		}),
		retry.WithAttributes(attribute.String("kcp.cluster", t.cluster)),
		retry.WithBeforeRetry(func() {
			for _, b := range dead {
				_ = b.Close() // a close's own error is not a result; the next lookup reopens the broker
			}
			if err := t.client.RefreshMetadata(refresh...); err != nil {
				slog.Warn("⚠️ could not refresh metadata before retrying the offset fetch", "cluster", t.cluster, "error", err)
			}
		}),
	)
	if err != nil {
		return nil, err
	}
	slog.Debug("fetched offsets for topic batch", "topics", len(topics))
	return results, nil
}

// retryableUnread reports whether fetching failed again can succeed: every partition's cause is stale metadata or
// a connection-level failure.
func retryableUnread(failed []failedPartition) bool {
	for _, f := range failed {
		if !retryableCause(f.cause) {
			return false
		}
	}
	return true
}

// retryableCause reports whether a partition's cause is stale metadata (a leader lookup or offset block that
// failed with one of isStaleMetadataErr's errors) or a connection-level failure (retry.Retryable).
func retryableCause(err error) bool {
	var kerr sarama.KError
	if errors.As(err, &kerr) && isStaleMetadataErr(kerr) {
		return true
	}
	return retry.Retryable(err)
}

// retryPlan is what the next attempt needs after a sweep left failed unread: the partitions to fetch again, the
// leaders whose connections failed (to close first) and the topics whose metadata to refresh.
func retryPlan(failed []failedPartition) (pending []topicPartition, dead []*sarama.Broker, refresh []string) {
	seenTopic := map[string]bool{}
	seenBroker := map[*sarama.Broker]bool{}
	for _, f := range failed {
		pending = append(pending, f.tp)
		if !seenTopic[f.tp.topic] {
			seenTopic[f.tp.topic] = true
			refresh = append(refresh, f.tp.topic)
		}
		if f.broker != nil && !seenBroker[f.broker] {
			seenBroker[f.broker] = true
			dead = append(dead, f.broker)
		}
	}
	return pending, dead, refresh
}

// fetchBatch groups partitions by leader broker, issues one ListOffsets
// request per broker, and writes offsets into results. Partitions whose
// leader lookup failed, whose offset block carried a stale-metadata error,
// or whose broker request failed at the transport level are returned (with
// their cause) for the caller, which retries those retryableCause accepts; any
// other failure aborts the batch.
func (t *Service) fetchBatch(ctx context.Context, tps []topicPartition, results map[string]map[int32]int64) ([]failedPartition, error) {
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

	tracing.SetAttributes(ctx, attribute.Int("kcp.brokers", len(brokerPartitions)))

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
				// A transport-level failure (connection refused, reset, EOF from a restarting broker) is the
				// blunt form of stale metadata: the cached leader is gone, or its connection is. Every partition
				// routed to this broker is retried by getMany after the broker is closed and metadata refreshed,
				// when the failure is connection-level (retryableCause); a failed login or TLS error is not retried.
				mu.Lock()
				for _, tp := range parts {
					failed = append(failed, failedPartition{
						tp:     tp,
						cause:  fmt.Errorf("offset request to broker %s failed for %s/%d: %w", broker.Addr(), tp.topic, tp.partition, err),
						broker: broker,
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
