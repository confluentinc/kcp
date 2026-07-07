// Offset-fetch benchmark used by the migration e2e tests.
//
// Ensures --topics topics exist on the cluster (creating any that are
// missing in batched CreateTopics requests), then times fetching every
// topic's log-end offsets two ways through internal/services/offset — the
// exact code the migration workflow uses for its lag-check and promote
// sweeps:
//
//	loop  — Service.Get per topic, serially: one ListOffsets round trip
//	        per topic (the pre-optimization workflow behavior)
//	batch — Service.GetMany over all topics: one ListOffsets request per
//	        leader broker (the current workflow behavior)
//
// Both sweeps must return identical offsets; the result is printed as a
// single JSON object on stdout. Plaintext unauthenticated — matches the
// e2e fixture cluster posture.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/offset"
)

const createChunkSize = 500

type result struct {
	Topics          int     `json:"topics"`
	PartitionsTotal int     `json:"partitions_total"`
	LoopMs          int64   `json:"loop_ms"`
	LoopPerTopicUs  int64   `json:"loop_per_topic_us"`
	BatchUs         int64   `json:"batch_us"`
	Speedup         float64 `json:"speedup"`
	OffsetsMatch    bool    `json:"offsets_match"`
}

func main() {
	var (
		bootstrap  = flag.String("bootstrap", "", "comma-separated Kafka bootstrap servers (required)")
		topicCount = flag.Int("topics", 1000, "number of benchmark topics to ensure and sweep (min 1)")
		partitions = flag.Int("partitions", 1, "partitions per benchmark topic")
		prefix     = flag.String("prefix", "kcp-offsetbench-", "benchmark topic name prefix")
	)
	flag.Parse()

	if *bootstrap == "" {
		log.Fatal("--bootstrap is required")
	}
	if *topicCount < 1 {
		log.Fatalf("--topics must be at least 1, got %d", *topicCount)
	}

	// Same client constructor and settings as production offset fetching.
	kafkaClient, err := client.NewKafkaClient(strings.Split(*bootstrap, ","), "",
		client.WithUnauthenticatedPlaintextAuth())
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer kafkaClient.Close()

	ctx := context.Background()

	topics := topicNames(*prefix, *topicCount)
	if err := ensureTopics(kafkaClient, topics, int32(*partitions)); err != nil {
		log.Fatalf("failed to ensure benchmark topics: %v", err)
	}

	svc := offset.NewOffsetService(kafkaClient)
	if err := awaitTopicsFetchable(ctx, svc, topics); err != nil {
		log.Fatalf("failed waiting for topics to become fetchable: %v", err)
	}

	res := result{Topics: *topicCount}

	// The pre-optimization workflow sweep: one Get (one ListOffsets round
	// trip) per topic, serially, over a warm client.
	loopStart := time.Now()
	loopOffsets := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		offsets, err := svc.Get(ctx, topic)
		if err != nil {
			log.Fatalf("loop sweep failed for topic %s: %v", topic, err)
		}
		loopOffsets[topic] = offsets
	}
	loopDur := time.Since(loopStart)
	res.LoopMs = loopDur.Milliseconds()
	res.LoopPerTopicUs = loopDur.Microseconds() / int64(len(topics))
	for _, offsets := range loopOffsets {
		res.PartitionsTotal += len(offsets)
	}

	// The batched sweep the workflow now uses.
	batchStart := time.Now()
	batchOffsets, err := svc.GetMany(ctx, topics)
	if err != nil {
		log.Fatalf("batch sweep failed: %v", err)
	}
	batchDur := time.Since(batchStart)
	res.BatchUs = batchDur.Microseconds()

	// Compute the ratio from the raw durations — millisecond truncation
	// would zero out a sub-1ms batched sweep and report a bogus speedup.
	if batchDur > 0 {
		res.Speedup = float64(loopDur) / float64(batchDur)
	}

	// A mismatch is reported in the JSON rather than aborting, so the e2e
	// test's assertion on it can actually fire and print both timings.
	res.OffsetsMatch = maps.EqualFunc(loopOffsets, batchOffsets, maps.Equal)

	out, err := json.Marshal(res)
	if err != nil {
		log.Fatalf("failed to marshal result: %v", err)
	}
	fmt.Println(string(out))
}

func topicNames(prefix string, n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("%s%04d", prefix, i)
	}
	return names
}

// ensureTopics creates any missing benchmark topics in chunked CreateTopics
// requests against the controller. Already-existing topics are fine —
// reruns against a warm environment are expected.
func ensureTopics(kafkaClient sarama.Client, topics []string, partitions int32) error {
	controller, err := kafkaClient.Controller()
	if err != nil {
		return fmt.Errorf("failed to get controller: %w", err)
	}

	for start := 0; start < len(topics); start += createChunkSize {
		end := min(start+createChunkSize, len(topics))
		details := make(map[string]*sarama.TopicDetail, end-start)
		for _, topic := range topics[start:end] {
			details[topic] = &sarama.TopicDetail{
				NumPartitions:     partitions,
				ReplicationFactor: 1,
			}
		}

		resp, err := controller.CreateTopics(&sarama.CreateTopicsRequest{
			Version:      2,
			TopicDetails: details,
			Timeout:      60 * time.Second,
		})
		if err != nil {
			return fmt.Errorf("CreateTopics request failed: %w", err)
		}
		for topic, topicErr := range resp.TopicErrors {
			if topicErr.Err != sarama.ErrNoError && topicErr.Err != sarama.ErrTopicAlreadyExists {
				return fmt.Errorf("failed to create topic %s: %v", topic, topicErr.Err)
			}
		}
		fmt.Fprintf(os.Stderr, "ensured topics %d-%d\n", start, end-1)
	}
	return nil
}

// awaitTopicsFetchable runs untimed warm-up sweeps until offsets for every
// benchmark topic can actually be fetched. On a fresh environment the
// just-created topics may not be in metadata yet, and leader elections for
// hundreds of new partitions can still be settling — either would fail (or
// skew) the timed sweeps with propagation noise that has nothing to do with
// what this benchmark measures. GetMany surfaces both cases as errors, so a
// clean sweep certifies readiness end to end.
func awaitTopicsFetchable(ctx context.Context, svc *offset.Service, topics []string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		_, err := svc.GetMany(ctx, topics)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("topics still not fetchable at deadline: %w", err)
		}
		fmt.Fprintf(os.Stderr, "warm-up sweep not yet clean: %v\n", err)
		time.Sleep(2 * time.Second)
	}
}
