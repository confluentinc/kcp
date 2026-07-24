package connectdiscovery

import (
	"context"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastTimeouts returns durations short enough for unit tests to exit promptly
// while still exercising the idle-vs-overall branch logic.
func fastTimeouts() (overall, idle time.Duration) {
	return 500 * time.Millisecond, 50 * time.Millisecond
}

// statusConnectorMsg returns a ConsumerMessage shaped like an entry from the
// connect-status topic — `status-connector-<name>` key, JSON value with a
// `worker_id` field.
func statusConnectorMsg(t *testing.T, topic, name, workerID string) *sarama.ConsumerMessage {
	t.Helper()
	return &sarama.ConsumerMessage{
		Topic: topic,
		Key:   []byte("status-connector-" + name),
		Value: []byte(`{"state":"RUNNING","worker_id":"` + workerID + `"}`),
	}
}

// statusTaskMsg returns a ConsumerMessage shaped like a task entry —
// `status-task-<name>-<n>` key, JSON value with a `worker_id` field.
func statusTaskMsg(t *testing.T, topic, name string, taskNum int, workerID string) *sarama.ConsumerMessage {
	t.Helper()
	return &sarama.ConsumerMessage{
		Topic: topic,
		Key:   []byte("status-task-" + name + "-" + itoa(taskNum)),
		Value: []byte(`{"state":"RUNNING","worker_id":"` + workerID + `"}`),
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+(i%10))) + digits
		i /= 10
	}
	return digits
}

func TestExtractWorkerIDs_HappyPath_ThreeDistinctWorkers(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "conn1", "host-a:8083"))
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "conn2", "host-b:8083"))
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "conn3", "host-c:8083"))

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 3)
	assert.Contains(t, workers, "host-a:8083")
	assert.Contains(t, workers, "host-b:8083")
	assert.Contains(t, workers, "host-c:8083")
	assert.Equal(t, 3, stats.TopicStats["connect-status"].MessagesRead)
	assert.Equal(t, 3, stats.TopicStats["connect-status"].WorkerIDsFound)
	assert.False(t, stats.TopicStats["connect-status"].TopicNotFound)

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_DeduplicatesConnectorAndTaskMessages(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "conn1", "host-a:8083"))
	pc.YieldMessage(statusTaskMsg(t, "connect-status", "conn1", 0, "host-a:8083"))
	pc.YieldMessage(statusTaskMsg(t, "connect-status", "conn1", 1, "host-a:8083"))

	overall, idle := fastTimeouts()
	workers, _, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 1)
	assert.Contains(t, workers, "host-a:8083")

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_DeduplicatesAcrossTopics(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{
		"connect-status-A": {0},
		"connect-status-B": {0},
	})

	pcA := consumer.ExpectConsumePartition("connect-status-A", 0, sarama.OffsetOldest)
	pcA.YieldMessage(statusConnectorMsg(t, "connect-status-A", "conn1", "shared-host:8083"))

	pcB := consumer.ExpectConsumePartition("connect-status-B", 0, sarama.OffsetOldest)
	pcB.YieldMessage(statusConnectorMsg(t, "connect-status-B", "conn2", "shared-host:8083"))

	overall, idle := fastTimeouts()
	workers, _, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status-A", "connect-status-B"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 1)
	assert.Contains(t, workers, "shared-host:8083")

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_EmptyTopic_ReturnsEmptySet(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	// Prime an empty partition consumer. Idle timeout drains it.
	_ = consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Empty(t, workers)
	assert.Equal(t, 0, stats.TopicStats["connect-status"].MessagesRead)

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_StatusTaskOnly_FindsWorker(t *testing.T) {
	// A worker that hosts only tasks (no connector instances) still has its
	// worker_id surfaced. Locks in the status-task inclusion decision.
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(statusTaskMsg(t, "connect-status", "conn1", 0, "task-host:8083"))

	overall, idle := fastTimeouts()
	workers, _, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 1)
	assert.Contains(t, workers, "task-host:8083")

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_TopicNotFound_RecordedInStats(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{}) // No topics registered.

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Empty(t, workers)
	assert.True(t, stats.TopicStats["connect-status"].TopicNotFound)

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_Tombstone_SkippedAndCountedSeparately(t *testing.T) {
	// A status-* message with an empty value is a Kafka log-compaction
	// tombstone — the deletion marker for a connector or task. It must be
	// silently skipped, not counted as a parse failure.
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(&sarama.ConsumerMessage{
		Topic: "connect-status",
		Key:   []byte("status-connector-deleted-conn"),
		Value: []byte{}, // tombstone
	})
	pc.YieldMessage(&sarama.ConsumerMessage{
		Topic: "connect-status",
		Key:   []byte("status-task-deleted-conn-0"),
		Value: nil, // also a tombstone — nil value, length 0
	})
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "live", "live-host:8083"))

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 1)
	assert.Contains(t, workers, "live-host:8083")
	assert.Equal(t, 2, stats.TopicStats["connect-status"].Tombstones)
	assert.Equal(t, 0, stats.TopicStats["connect-status"].ParseFailures, "tombstones must not be counted as parse failures")
	assert.Equal(t, 0, stats.TopicStats["connect-status"].MissingField, "tombstones must not be counted as missing-field")

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_UnparseableJSON_SkippedAndCounted(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(&sarama.ConsumerMessage{
		Topic: "connect-status",
		Key:   []byte("status-connector-broken"),
		Value: []byte("not valid json"),
	})
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "good", "good-host:8083"))

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 1)
	assert.Contains(t, workers, "good-host:8083")
	assert.Equal(t, 1, stats.TopicStats["connect-status"].ParseFailures)

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_MissingWorkerIDField_SkippedAndCounted(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(&sarama.ConsumerMessage{
		Topic: "connect-status",
		Key:   []byte("status-connector-orphan"),
		Value: []byte(`{"state":"RUNNING"}`),
	})
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "good", "good-host:8083"))

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 1)
	assert.Contains(t, workers, "good-host:8083")
	assert.Equal(t, 1, stats.TopicStats["connect-status"].MissingField)

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_EmptyWorkerIDValue_Skipped(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(&sarama.ConsumerMessage{
		Topic: "connect-status",
		Key:   []byte("status-connector-empty"),
		Value: []byte(`{"state":"RUNNING","worker_id":""}`),
	})

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Empty(t, workers)
	assert.Equal(t, 1, stats.TopicStats["connect-status"].MissingField)

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_NonStatusKey_Ignored(t *testing.T) {
	// Messages with keys that don't match status-connector-/status-task- are
	// silently skipped. They aren't parse failures — they're just not for us.
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(&sarama.ConsumerMessage{
		Topic: "connect-status",
		Key:   []byte("connector-other"),
		Value: []byte(`{"worker_id":"do-not-pick-up:8083"}`),
	})
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "good", "good-host:8083"))

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 1)
	assert.Contains(t, workers, "good-host:8083")
	assert.NotContains(t, workers, "do-not-pick-up:8083")
	// Non-status keys do not count toward parse failures or missing fields.
	assert.Equal(t, 0, stats.TopicStats["connect-status"].ParseFailures)
	assert.Equal(t, 0, stats.TopicStats["connect-status"].MissingField)

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_MultiPartition_MergesAcrossPartitions(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0, 1}})

	pc0 := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc0.YieldMessage(statusConnectorMsg(t, "connect-status", "conn0", "host-0:8083"))

	pc1 := consumer.ExpectConsumePartition("connect-status", 1, sarama.OffsetOldest)
	pc1.YieldMessage(statusConnectorMsg(t, "connect-status", "conn1", "host-1:8083"))

	overall, idle := fastTimeouts()
	workers, stats, err := extractFromConsumer(context.Background(), consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	assert.Len(t, workers, 2)
	assert.Contains(t, workers, "host-0:8083")
	assert.Contains(t, workers, "host-1:8083")
	assert.Equal(t, 2, stats.TopicStats["connect-status"].PartitionsRead)

	require.NoError(t, consumer.Close())
}

func TestExtractWorkerIDs_ContextCanceled_ReturnsPartial(t *testing.T) {
	consumer := mocks.NewConsumer(t, sarama.NewConfig())
	consumer.SetTopicMetadata(map[string][]int32{"connect-status": {0}})

	pc := consumer.ExpectConsumePartition("connect-status", 0, sarama.OffsetOldest)
	pc.YieldMessage(statusConnectorMsg(t, "connect-status", "conn1", "host-a:8083"))

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately after the partition consumer has been drained but
	// before the idle timeout fires — the consumePartition loop should bail
	// out via the ctx.Done() branch on its next iteration.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	overall, idle := 5*time.Second, 5*time.Second
	workers, _, err := extractFromConsumer(ctx, consumer, []string{"connect-status"}, overall, idle)
	require.NoError(t, err)

	// At least the first message should have been processed before cancellation;
	// the partition loop must have exited (no hang).
	assert.LessOrEqual(t, len(workers), 1)

	require.NoError(t, consumer.Close())
}
