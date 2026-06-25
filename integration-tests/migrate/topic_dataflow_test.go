//go:build integration

package migrate

import (
	"fmt"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// sarama produce/consume helpers for the later data-flow topic tests (T4-T6).
//
// These talk to a broker's PLAINTEXT host listener directly (not via REST), so
// a test can write records to a source topic and later prove the mirrored data
// arrived on the destination.
//
// Version note: sarama.V2_8_0_0 is broadly compatible with the cp-server 8.x
// brokers in docker-compose.yml (the admin client negotiates anyway). T4 proves
// it live; if a broker rejects it, bump to a 3.x version that the brokers
// accept.
// ---------------------------------------------------------------------------

// dataflowKafkaVersion is the sarama protocol version used by the produce/consume
// helpers. Broadly compatible with cp-server 8.x brokers.
var dataflowKafkaVersion = sarama.V2_8_0_0

// produceRecords writes n records ("msg-0".."msg-(n-1)") to topic via a
// SyncProducer on the given PLAINTEXT bootstrap (e.g. "localhost:19092"). It
// fails the test on any error.
func produceRecords(t *testing.T, bootstrap, topic string, n int) {
	t.Helper()
	config := sarama.NewConfig()
	config.Version = dataflowKafkaVersion
	config.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer([]string{bootstrap}, config)
	require.NoError(t, err, "new sync producer on %s", bootstrap)
	defer func() { _ = producer.Close() }()

	for i := 0; i < n; i++ {
		msg := &sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder(fmt.Sprintf("msg-%d", i)),
		}
		_, _, err := producer.SendMessage(msg)
		require.NoError(t, err, "produce record %d to %s/%s", i, bootstrap, topic)
	}
}

// consumeRecords reads from topic on the given PLAINTEXT bootstrap starting at
// the oldest offset, returning up to want record values, giving up after
// timeout. Used to prove mirrored data arrived on the destination.
//
// It reads partition 0 only: the data-flow tests use a dedicated single-partition
// topic, so partition 0 holds every record.
func consumeRecords(t *testing.T, bootstrap, topic string, want int, timeout time.Duration) [][]byte {
	t.Helper()
	config := sarama.NewConfig()
	config.Version = dataflowKafkaVersion

	consumer, err := sarama.NewConsumer([]string{bootstrap}, config)
	require.NoError(t, err, "new consumer on %s", bootstrap)
	defer func() { _ = consumer.Close() }()

	pc, err := consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
	require.NoError(t, err, "consume partition 0 of %s/%s", bootstrap, topic)
	defer func() { _ = pc.Close() }()

	out := make([][]byte, 0, want)
	deadline := time.After(timeout)
	for len(out) < want {
		select {
		case msg, ok := <-pc.Messages():
			if !ok {
				return out
			}
			out = append(out, msg.Value)
		case <-deadline:
			return out
		}
	}
	return out
}

// The produce/consume helpers are first called by the data-flow topic tests
// (T4-T6). Until then, anchor compile-time references so the `unused` linter
// does not flag them; the references cost nothing at runtime.
var (
	_ = produceRecords
	_ = consumeRecords
)
