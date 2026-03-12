package offset

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/IBM/sarama"
)

// TopicOffsets holds per-partition LEO for a topic.
type TopicOffsets struct {
	Topic  string
	Source map[int32]int64
	Dest   map[int32]int64
}

// GetTopicOffsets fetches the log end offset (LEO) for every partition of a topic.
// Requests are batched by leader broker for efficiency.
func GetTopicOffsets(client sarama.Client, topic string) (map[int32]int64, error) {
	partitions, err := client.Partitions(topic)
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions for topic %q: %w", topic, err)
	}

	slog.Debug("fetching offsets", "topic", topic, "partitions", len(partitions))

	brokerPartitions := make(map[*sarama.Broker][]int32)
	for _, p := range partitions {
		leader, err := client.Leader(topic, p)
		if err != nil {
			return nil, fmt.Errorf("failed to get leader for %s/%d: %w", topic, p, err)
		}
		brokerPartitions[leader] = append(brokerPartitions[leader], p)
	}

	offsets := make(map[int32]int64, len(partitions))
	for broker, parts := range brokerPartitions {
		req := &sarama.OffsetRequest{
			Version: 1,
		}
		for _, p := range parts {
			req.AddBlock(topic, p, sarama.OffsetNewest, 1)
		}

		resp, err := broker.GetAvailableOffsets(req)
		if err != nil {
			return nil, fmt.Errorf("failed to get offsets from broker %s: %w", broker.Addr(), err)
		}

		for _, p := range parts {
			block := resp.GetBlock(topic, p)
			if block == nil {
				return nil, fmt.Errorf("no offset block returned for %s/%d", topic, p)
			}
			if block.Err != sarama.ErrNoError {
				return nil, fmt.Errorf("offset error for %s/%d: %v", topic, p, block.Err)
			}
			offsets[p] = block.Offset
		}
	}

	slog.Debug("fetched offsets", "topic", topic, "partitions", len(offsets))
	return offsets, nil
}

// TopicExists checks whether a topic exists on the cluster by refreshing metadata.
func TopicExists(client sarama.Client, topic string) (bool, error) {
	if err := client.RefreshMetadata(); err != nil {
		return false, fmt.Errorf("failed to refresh metadata: %w", err)
	}
	topics, err := client.Topics()
	if err != nil {
		return false, fmt.Errorf("failed to list topics: %w", err)
	}
	for _, t := range topics {
		if t == topic {
			return true, nil
		}
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
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
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
