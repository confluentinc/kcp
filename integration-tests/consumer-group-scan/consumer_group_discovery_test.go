//go:build integration

package consumer_group_scan

// Integration matrix: runs the PRODUCTION consumer-group discovery path against
// five real single-node KRaft brokers, one per group-protocol milestone, so
// every group type is validated on the broker version where it is real:
//
//   kafka-3.7 — ListGroups maxes at v4  -> no type (v5->v4 fallback cell)
//   kafka-3.8 — ListGroups v5           -> classic groups report Type=="classic"
//   kafka-4.0 — KIP-848 GA              -> a "consumer"-protocol group -> Type=="consumer"
//   kafka-4.1 — KIP-932 GA              -> a "share" group             -> Type=="share"
//   kafka-4.2 — KIP-1071 GA             -> a "streams" group           -> Type=="streams"
//
// setup.sh brings the brokers up (and launches the Streams app for the 4.2
// cell, since there is no console tool for streams groups); the Makefile target
// (test-consumer-group-scan) runs setup before `go test` and teardown after.
//
// The discovery helper mirrors the production type-dispatch (see
// internal/services/kafka/kafka_service.go scanConsumerGroups): only classic and
// unknown-type groups are described via the classic DescribeGroups API. For the
// KIP-848/932/1071 types that API returns a misleading "Dead"/empty answer, so
// they are NOT described and keep their accurate ListGroups v5 state with no
// members.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/kafka"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isClassicDescribable mirrors the production dispatch rule
// (internal/services/kafka/kafka_service.go): only classic and unknown-type
// groups are meaningfully describable via the classic DescribeGroups API.
func isClassicDescribable(groupType string) bool {
	return groupType == "" || groupType == types.ConsumerGroupTypeClassic
}

// discoverGroupsErr runs the production discovery path end-to-end against a live
// broker and returns the resulting domain model (or an error). It is safe to
// poll from a require.Eventually condition (which testify runs on its own
// goroutine, where t.FailNow is unsafe). It reproduces exactly what the
// production collector does: list groups with their type, describe ONLY the
// classic/unknown ones, resolve coordinators for all, then map.
func discoverGroupsErr(bootstrap string) (*types.ConsumerGroups, error) {
	cg, err := client.NewConsumerGroupClient([]string{bootstrap}, "", client.WithUnauthenticatedPlaintextAuth())
	if err != nil {
		return nil, err
	}
	defer func() { _ = cg.Close() }()

	listings, err := cg.ListGroupsWithType()
	if err != nil {
		return nil, err
	}

	allIDs := make([]string, 0, len(listings))
	describableIDs := make([]string, 0, len(listings))
	for _, l := range listings {
		allIDs = append(allIDs, l.GroupID)
		if isClassicDescribable(l.Type) {
			describableIDs = append(describableIDs, l.GroupID)
		}
	}

	descs, err := cg.DescribeGroups(describableIDs)
	if err != nil {
		return nil, err
	}

	coords := cg.Coordinators(allIDs)
	return kafka.BuildConsumerGroups(listings, descs, coords), nil
}

// classicHandler is a minimal sarama consumer-group handler; discovery only
// cares that the group is a real, live classic member.
type classicHandler struct{}

func (classicHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (classicHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (classicHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		sess.MarkMessage(msg, "")
	}
	return nil
}

// ensureTopic creates topic (idempotently) via a plain sarama admin client
// pinned at 3.6.0 — a client protocol version every broker in the matrix speaks.
func ensureTopic(t *testing.T, bootstrap, topic string, partitions int32) {
	t.Helper()

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	admin, err := sarama.NewClusterAdmin([]string{bootstrap}, cfg)
	require.NoError(t, err)
	defer func() { _ = admin.Close() }()

	err = admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: partitions, ReplicationFactor: 1}, false)
	if err != nil && !errors.Is(err, sarama.ErrTopicAlreadyExists) {
		require.NoError(t, err, "failed to create topic %q", topic)
	}
}

// produceRecords writes a few records so a joining group has something to
// consume, keeping its member actively subscribed while discovery polls.
func produceRecords(t *testing.T, bootstrap, topic string, n int) {
	t.Helper()
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	cfg.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer([]string{bootstrap}, cfg)
	require.NoError(t, err)
	defer func() { _ = producer.Close() }()
	for i := 0; i < n; i++ {
		_, _, err := producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder(fmt.Sprintf("msg-%d", i)),
		})
		require.NoError(t, err)
	}
}

// pollForTypedGroup polls the production discovery path until groupID appears
// with the expected KIP-848 type and a Stable state, failing after timeout.
func pollForTypedGroup(t *testing.T, bootstrap, groupID, expectedType string, timeout time.Duration) types.ConsumerGroupDetails {
	t.Helper()
	var found types.ConsumerGroupDetails
	require.Eventually(t, func() bool {
		cgs, err := discoverGroupsErr(bootstrap)
		if err != nil {
			return false
		}
		for _, d := range cgs.Details {
			if d.GroupID == groupID && d.Type == expectedType && strings.Contains(d.State, "Stable") {
				found = d
				return true
			}
		}
		return false
	}, timeout, 2*time.Second, "%q never appeared with type=%q and a Stable state", groupID, expectedType)
	return found
}

// runClassicGroupCell exercises the classic-group path on one broker: a live
// classic consumer member, discovered and asserted with full detail (members,
// client host, decoded assigned topic). The type field lands exactly where the
// v5/v4 boundary dictates for the broker version.
func runClassicGroupCell(t *testing.T, bootstrap string, typeSupported bool) {
	t.Helper()

	const topic = "orders"
	const groupID = "orders-classic"

	ensureTopic(t, bootstrap, topic, 3)

	consumerCfg := sarama.NewConfig()
	consumerCfg.Version = sarama.V3_6_0_0
	consumerCfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cg, err := sarama.NewConsumerGroup([]string{bootstrap}, groupID, consumerCfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	handler := classicHandler{}
	var consumeWG sync.WaitGroup
	consumeWG.Add(1)
	go func() {
		defer consumeWG.Done()
		for {
			if err := cg.Consume(ctx, []string{topic}, handler); err != nil && !errors.Is(err, sarama.ErrClosedConsumerGroup) {
				t.Logf("orders-classic consume loop error (may be transient during (re)join): %v", err)
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		_ = cg.Close()
		consumeWG.Wait()
	}()

	produceRecords(t, bootstrap, topic, 5)

	var found types.ConsumerGroupDetails
	require.Eventually(t, func() bool {
		cgs, err := discoverGroupsErr(bootstrap)
		if err != nil {
			return false
		}
		for _, d := range cgs.Details {
			if d.GroupID == groupID {
				found = d
				return strings.Contains(d.State, "Stable") && len(d.Members) >= 1
			}
		}
		return false
	}, 40*time.Second, 2*time.Second, "%q never reached Stable state with an active member", groupID)

	if typeSupported {
		assert.Equal(t, types.ConsumerGroupTypeClassic, found.Type, "broker speaks ListGroups v5; expected type=classic")
	} else {
		assert.Equal(t, "", found.Type, "broker maxes out at ListGroups v4; expected no type (fallback)")
	}

	require.GreaterOrEqual(t, len(found.Members), 1, "expected at least one live member")
	for _, m := range found.Members {
		assert.NotEmpty(t, m.ClientHost, "member ClientHost should be populated")
	}
	assert.Contains(t, found.Topics, topic, "classic assignment should decode on every broker version")
}

// startConsoleConsumer starts one of the broker's own console consumers (which
// speak the newer client protocols sarama cannot) via docker exec, returning a
// cancel func. protocol is "" for a classic console consumer or "consumer" for
// a KIP-848 consumer-protocol one.
func startConsoleConsumer(t *testing.T, ctx context.Context, container, internalBootstrap, topic, groupID, protocol string) *exec.Cmd {
	t.Helper()
	args := []string{"exec", container, "/opt/kafka/bin/kafka-console-consumer.sh",
		"--bootstrap-server", internalBootstrap, "--topic", topic, "--group", groupID, "--from-beginning"}
	if protocol != "" {
		args = append(args, "--consumer-property", "group.protocol="+protocol)
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	require.NoError(t, cmd.Start(), "failed to start console consumer for %q", groupID)
	return cmd
}

// TestConsumerGroupDiscoveryMatrix runs the classic cells across the version
// boundary, plus one dedicated cell per non-classic type on the broker version
// where that type is GA.
func TestConsumerGroupDiscoveryMatrix(t *testing.T) {
	// --- classic cells: the v5/v4 type boundary ---
	classicBrokers := []struct {
		name          string
		bootstrap     string
		typeSupported bool
	}{
		{"kafka-3.7-classic", "localhost:39092", false},
		{"kafka-3.8-classic", "localhost:39093", true},
		{"kafka-4.0-classic", "localhost:39094", true},
	}
	for _, b := range classicBrokers {
		b := b
		t.Run(b.name, func(t *testing.T) {
			runClassicGroupCell(t, b.bootstrap, b.typeSupported)
		})
	}

	// --- consumer cell (KIP-848, GA in 4.0) ---
	// A consumer-protocol group. Post-fix, discovery reports type=consumer with
	// the correct ListGroups v5 state (Stable, NOT the classic API's "Dead") and
	// NO members (its members live only in the type-specific ConsumerGroupDescribe
	// API, which the client does not implement).
	t.Run("kafka-4.0-consumer", func(t *testing.T) {
		const bootstrap = "localhost:39094"
		ensureTopic(t, bootstrap, "orders", 3)
		produceRecords(t, bootstrap, "orders", 5)
		ctx, cancel := context.WithCancel(context.Background())
		cmd := startConsoleConsumer(t, ctx, "kcp-cg-kafka-40", "kafka-40:29092", "orders", "orders-consumer", "consumer")
		defer func() { cancel(); _ = cmd.Wait() }()

		found := pollForTypedGroup(t, bootstrap, "orders-consumer", types.ConsumerGroupTypeConsumer, 60*time.Second)
		assert.Equal(t, types.ConsumerGroupTypeConsumer, found.Type)
		assert.Contains(t, found.State, "Stable", "consumer group must keep its ListGroups v5 state, not the classic-API 'Dead'")
		assert.Empty(t, found.Members, "consumer groups get no member detail from the classic describe API (design §14)")
	})

	// --- share cell (KIP-932, GA in 4.1) ---
	t.Run("kafka-4.1-share", func(t *testing.T) {
		const bootstrap = "localhost:39095"
		ensureTopic(t, bootstrap, "orders", 3)
		produceRecords(t, bootstrap, "orders", 5)
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, "docker", "exec", "kcp-cg-kafka-41",
			"/opt/kafka/bin/kafka-console-share-consumer.sh",
			"--bootstrap-server", "kafka-41:29092", "--topic", "orders", "--group", "orders-share")
		require.NoError(t, cmd.Start(), "failed to start share consumer")
		defer func() { cancel(); _ = cmd.Wait() }()

		found := pollForTypedGroup(t, bootstrap, "orders-share", types.ConsumerGroupTypeShare, 60*time.Second)
		assert.Equal(t, types.ConsumerGroupTypeShare, found.Type)
		assert.Contains(t, found.State, "Stable")
		assert.Empty(t, found.Members, "share groups get no member detail from the classic describe API")
	})

	// --- streams cell (KIP-1071, GA in 4.2) ---
	// The streams group is formed by the Streams app that setup.sh compiles and
	// launches (there is no console tool for streams groups). This cell only
	// scans and asserts; it will fail if setup did not launch the app.
	t.Run("kafka-4.2-streams", func(t *testing.T) {
		const bootstrap = "localhost:39096"
		found := pollForTypedGroup(t, bootstrap, "streams-grp", types.ConsumerGroupTypeStreams, 60*time.Second)
		assert.Equal(t, types.ConsumerGroupTypeStreams, found.Type)
		assert.Contains(t, found.State, "Stable")
		assert.Empty(t, found.Members, "streams groups get no member detail from the classic describe API")
	})
}
