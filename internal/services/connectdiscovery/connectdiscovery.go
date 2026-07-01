// Package connectdiscovery extracts Kafka Connect worker URLs from the
// compacted status-storage topics that distributed-mode Connect workers write
// to. Each status message — keyed `status-connector-<name>` for connectors
// and `status-task-<name>-<task#>` for tasks — carries a JSON value containing
// a `worker_id` field that points back at the Connect worker's REST listener
// (typically `host:port`). Collecting the unique `worker_id` values across
// one or more status topics yields the set of Connect workers backing a given
// Kafka cluster.
//
// This package is the runtime of `kcp scan connect-topics` and is the
// narrower successor of the topic-parsing connector discovery removed in
// PR #289 — it only extracts `worker_id`s, not full connector configs or
// statuses.
package connectdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

const (
	keyPrefixStatusConnector = "status-connector-"
	keyPrefixStatusTask      = "status-task-"
)

// defaultOverallTimeout caps how long we wait on a single partition. Compacted
// status topics rarely take more than a handful of seconds to drain, but a
// long-running cluster may have many compacted entries. 30s is the same value
// the pre-PR-289 implementation used and is documented as MVP-grade.
const defaultOverallTimeout = 30 * time.Second

// defaultIdleTimeout terminates a partition consumer once no message has
// arrived for this duration — the signal that the partition has been fully
// drained. Same value as the pre-PR-289 implementation.
const defaultIdleTimeout = 5 * time.Second

// TopicStats records per-topic counters surfaced via ExtractStats. Callers
// (the scanner CLI) use these to warn cleanly when a topic produced zero
// `worker_id`s, and to distinguish "topic does not exist" from "topic exists
// but yielded nothing".
type TopicStats struct {
	TopicNotFound  bool // True when listing partitions returned ErrUnknownTopicOrPartition.
	PartitionsRead int  // Number of partitions we successfully consumed.
	MessagesRead   int  // Total messages observed across all partitions.
	WorkerIDsFound int  // Pre-dedup count of worker_id values extracted.
	Tombstones     int  // Status messages with empty value — Kafka compaction tombstones marking a deleted connector or task.
	ParseFailures  int  // Status messages whose non-empty JSON value failed to unmarshal.
	MissingField   int  // Status messages whose JSON unmarshalled but had no worker_id field.
}

// ExtractStats aggregates per-topic results from a single ExtractWorkerIDs
// call.
type ExtractStats struct {
	TopicStats map[string]TopicStats
}

// ExtractWorkerIDs consumes the named topics on the given brokers using the
// supplied sarama.Config and returns the unique set of `worker_id` values
// found in `status-connector-*` and `status-task-*` keyed messages, along
// with per-topic statistics.
//
// Per-topic and per-partition errors are reported via the returned
// ExtractStats and logs but do not abort the call — every topic that can be
// read is read. Only a failure to construct the underlying consumer (auth,
// broker reach, etc.) is surfaced as an error from this function.
func ExtractWorkerIDs(ctx context.Context, brokers []string, cfg *sarama.Config, topics []string) (map[string]struct{}, ExtractStats, error) {
	slog.Info("connecting to brokers", "brokers", brokers)
	consumer, err := sarama.NewConsumer(brokers, cfg)
	if err != nil {
		return nil, ExtractStats{}, fmt.Errorf("failed to create consumer: %w", err)
	}
	defer func() { _ = consumer.Close() }()
	slog.Info("connected; beginning topic scan", "topics", topics)

	return extractFromConsumer(ctx, consumer, topics, defaultOverallTimeout, defaultIdleTimeout)
}

// extractFromConsumer is the test seam — it accepts a pre-built sarama.Consumer
// (production uses sarama.NewConsumer; tests use sarama/mocks.NewConsumer) and
// configurable timeouts so unit tests can exit quickly.
func extractFromConsumer(ctx context.Context, consumer sarama.Consumer, topics []string, overallTimeout, idleTimeout time.Duration) (map[string]struct{}, ExtractStats, error) {
	workerIDs := make(map[string]struct{})
	stats := ExtractStats{TopicStats: make(map[string]TopicStats, len(topics))}

	for _, topic := range topics {
		ts := readTopic(ctx, consumer, topic, workerIDs, overallTimeout, idleTimeout)
		stats.TopicStats[topic] = ts
	}

	return workerIDs, stats, nil
}

// readTopic consumes all partitions of a single topic and merges any
// extracted worker_id values into out. Returns the per-topic stats.
func readTopic(ctx context.Context, consumer sarama.Consumer, topic string, out map[string]struct{}, overallTimeout, idleTimeout time.Duration) TopicStats {
	var stats TopicStats

	partitions, err := consumer.Partitions(topic)
	if err != nil {
		if errors.Is(err, sarama.ErrUnknownTopicOrPartition) {
			slog.Warn("topic not found on cluster", "topic", topic)
			stats.TopicNotFound = true
			return stats
		}
		slog.Warn("failed to list partitions for topic", "topic", topic, "error", err)
		return stats
	}

	if len(partitions) == 0 {
		slog.Info("topic has zero partitions; nothing to read", "topic", topic)
		return stats
	}

	slog.Info("reading topic", "topic", topic, "partitions", len(partitions))

	// TODO(perf): partitions (and topics, in extractFromConsumer) are drained
	// sequentially, so every partition pays the full idleTimeout wait — an
	// N-partition status topic takes ~N*idleTimeout even when empty. Draining
	// partitions concurrently (errgroup writing into out/stats under a mutex)
	// would bound this to ~idleTimeout. Deferred as MVP-grade; see PR #295.
	for _, partition := range partitions {
		pc, err := consumer.ConsumePartition(topic, partition, sarama.OffsetOldest)
		if err != nil {
			slog.Warn("failed to consume partition", "topic", topic, "partition", partition, "error", err)
			continue
		}
		stats.PartitionsRead++

		// Snapshot per-partition deltas so the progress log is meaningful when
		// many partitions share a topic.
		before := stats
		started := time.Now()
		consumePartition(ctx, pc, &stats, out, overallTimeout, idleTimeout)
		_ = pc.Close()

		slog.Info("finished partition",
			"topic", topic,
			"partition", partition,
			"elapsed_ms", time.Since(started).Milliseconds(),
			"messages_read", stats.MessagesRead-before.MessagesRead,
			"worker_ids_found", stats.WorkerIDsFound-before.WorkerIDsFound,
			"tombstones", stats.Tombstones-before.Tombstones,
		)
	}
	return stats
}

// consumePartition reads messages from a partition consumer until: the overall
// timeout fires, the idle timeout fires (no message for `idleTimeout`), the
// partition consumer reports an error, or the context is cancelled.
func consumePartition(ctx context.Context, pc sarama.PartitionConsumer, stats *TopicStats, out map[string]struct{}, overallTimeout, idleTimeout time.Duration) {
	overall := time.After(overallTimeout)

	// A single idle timer, reset on every message, rather than a fresh
	// time.After per loop iteration. When it fires, idleTimeout has elapsed
	// with no message — the signal that the partition is fully drained.
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-pc.Messages():
			if msg == nil {
				continue
			}
			idle.Reset(idleTimeout)
			stats.MessagesRead++

			workerID, ok := extractWorkerID(msg, stats)
			if !ok {
				continue
			}
			stats.WorkerIDsFound++
			out[workerID] = struct{}{}
		case err := <-pc.Errors():
			if err != nil {
				slog.Warn("partition consumer error", "error", err)
				return
			}
		case <-overall:
			return
		case <-idle.C:
			return
		}
	}
}

// extractWorkerID inspects a single sarama.ConsumerMessage and returns the
// `worker_id` value if (a) the key starts with `status-connector-` or
// `status-task-`, (b) the value is non-empty (not a deletion tombstone), and
// (c) the value JSON-unmarshals to an object containing a non-empty string
// `worker_id`. Updates per-topic counters in `stats` for observability —
// tombstones, parse failures, and missing fields are tracked separately so
// operators can tell deleted connectors apart from corrupt status messages.
// Returns ok=false when the message should be silently skipped (wrong key
// prefix) or noted-and-skipped (tombstone, parse failure, missing field).
func extractWorkerID(msg *sarama.ConsumerMessage, stats *TopicStats) (string, bool) {
	keyStr := string(msg.Key)
	if !strings.HasPrefix(keyStr, keyPrefixStatusConnector) && !strings.HasPrefix(keyStr, keyPrefixStatusTask) {
		return "", false
	}

	// An empty value on a compacted topic is a tombstone — Kafka's signal that
	// the connector or task identified by this key was deleted. Not a parse
	// failure; not corrupt data; just history. Counted in stats; no per-message
	// log line (they're noisy and confusing for the operator).
	if len(msg.Value) == 0 {
		stats.Tombstones++
		return "", false
	}

	var payload map[string]any
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		stats.ParseFailures++
		slog.Debug("status message JSON parse failed", "key", keyStr, "error", err)
		return "", false
	}

	workerID, ok := payload["worker_id"].(string)
	if !ok || workerID == "" {
		stats.MissingField++
		slog.Debug("status message missing worker_id", "key", keyStr)
		return "", false
	}
	return workerID, true
}
