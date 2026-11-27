//go:build e2e

package migplan_reversible_e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/stretchr/testify/require"
)

// The docker env setup.sh provisions.
const (
	sourceClusterID = "6ub6fPVJRzKjE4i-REkq-A"
	destClusterID   = "LKsbYRvfTM-TVXKjdjgdxA"
	sourceREST      = "http://localhost:18190"
	destREST        = "http://localhost:28190"
	sourceBroker    = "localhost:19192"
	destBroker      = "localhost:29192"
	linkName        = "rev-link"
	route           = "migration-route"
	settleTimeout   = 90 * time.Second
)

type fakeSecretChecker struct{}

func (fakeSecretChecker) MissingSecrets(context.Context, []string) ([]string, string, error) {
	return nil, "", nil
}

// loadManifest reads testdata/manifest.yaml, applies mutate, and requires the
// result to validate.
func loadManifest(t *testing.T, mutate func(*manifest.GatewayMigration)) *manifest.GatewayMigration {
	t.Helper()
	g, err := manifest.LoadGatewayMigrationFile("testdata/manifest.yaml")
	require.NoError(t, err)
	if mutate != nil {
		mutate(g)
	}
	require.Empty(t, g.Validate())
	return g
}

// reconcileWith runs migplan.Reconcile for g against the live clusters and
// link, with the gateway CR read from fixture, and returns the result and the
// rendered plan.
func reconcileWith(t *testing.T, g *manifest.GatewayMigration, fixture string) (*migplan.Result, string) {
	t.Helper()
	var buf bytes.Buffer
	res, err := migplan.Reconcile(context.Background(), g,
		migplan.WithGatewaySource(migplan.NewGatewayFile(fixture, route)),
		migplan.WithSecretExistenceChecker(fakeSecretChecker{}),
		migplan.WithOutput(&buf),
	)
	require.NoError(t, err)
	t.Logf("rendered plan:\n%s", buf.String())
	return res, buf.String()
}

func restDo(t *testing.T, method, url string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, out
}

func linkURL(rest, clusterID string) string {
	return fmt.Sprintf("%s/kafka/v3/clusters/%s/links/%s", rest, clusterID, linkName)
}

// reverse calls reverse-and-start on the cluster hosting the topics' mirrors.
func reverse(t *testing.T, rest, clusterID string, topics ...string) {
	t.Helper()
	code, body := restDo(t, http.MethodPost, linkURL(rest, clusterID)+"/mirrors:reverse-and-start-mirror",
		map[string]any{"mirror_topic_names": topics})
	require.Equal(t, http.StatusOK, code, "reverse-and-start on %s: %s", clusterID, body)
	var resp struct {
		Data []struct {
			MirrorTopicName string `json:"mirror_topic_name"`
			ErrorMessage    string `json:"error_message"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	for _, d := range resp.Data {
		require.Empty(t, d.ErrorMessage, "reverse-and-start of %s on %s", d.MirrorTopicName, clusterID)
	}
}

// mirrorStatus is a topic's row on one copy of the link: its mirror_status, or
// "none" when that copy has no row for it.
func mirrorStatus(t *testing.T, rest, clusterID, topic string) string {
	t.Helper()
	code, body := restDo(t, http.MethodGet, linkURL(rest, clusterID)+"/mirrors/"+topic, nil)
	if code == http.StatusNotFound {
		return "none"
	}
	require.Equal(t, http.StatusOK, code, "mirror %s on %s: %s", topic, clusterID, body)
	var m struct {
		MirrorStatus string `json:"mirror_status"`
	}
	require.NoError(t, json.Unmarshal(body, &m))
	return m.MirrorStatus
}

// waitRows waits until topic's row is wantSource on the source copy and
// wantTarget on the target copy.
func waitRows(t *testing.T, topic, wantSource, wantTarget string) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	var src, tgt string
	for time.Now().Before(deadline) {
		src, tgt = mirrorStatus(t, sourceREST, sourceClusterID, topic), mirrorStatus(t, destREST, destClusterID, topic)
		if src == wantSource && tgt == wantTarget {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s rows did not settle: source %s (want %s), target %s (want %s)", topic, src, wantSource, tgt, wantTarget)
}

// reverseAndSettle reverses topics on the copy hosting their mirrors, waits
// for the rows to settle, and registers the reversal back as cleanup.
func reverseAndSettle(t *testing.T, onSource bool, topics ...string) {
	t.Helper()
	there, back := []string{destREST, destClusterID}, []string{sourceREST, sourceClusterID}
	settled, restored := [2]string{"ACTIVE", "STOPPED"}, [2]string{"STOPPED", "ACTIVE"}
	if onSource {
		there, back = back, there
		settled, restored = restored, settled
	}
	reverse(t, there[0], there[1], topics...)
	for _, topic := range topics {
		waitRows(t, topic, settled[0], settled[1])
	}
	t.Cleanup(func() {
		reverse(t, back[0], back[1], topics...)
		for _, topic := range topics {
			waitRows(t, topic, restored[0], restored[1])
		}
	})
}

// alterLinkConfig sets one config on a copy of the link and registers the
// restore of its previous value as cleanup.
func alterLinkConfig(t *testing.T, rest, clusterID, key, value string) {
	t.Helper()
	code, body := restDo(t, http.MethodGet, linkURL(rest, clusterID)+"/configs/"+key, nil)
	require.Equal(t, http.StatusOK, code, "reading %s on %s: %s", key, clusterID, body)
	var cur struct {
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(body, &cur))

	set := func(v string) {
		code, body := restDo(t, http.MethodPut, linkURL(rest, clusterID)+"/configs:alter",
			map[string]any{"data": []map[string]string{{"name": key, "value": v}}})
		require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, code, "altering %s on %s: %s", key, clusterID, body)
	}
	set(value)
	t.Cleanup(func() { set(cur.Value) })
}

func saramaConfig() *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_6_0_0
	return cfg
}

// groupState is a consumer group's state on the cluster at broker.
func groupState(t *testing.T, broker, group string) string {
	t.Helper()
	admin, err := sarama.NewClusterAdmin([]string{broker}, saramaConfig())
	require.NoError(t, err)
	defer func() { _ = admin.Close() }()
	desc, err := admin.DescribeConsumerGroups([]string{group})
	require.NoError(t, err)
	require.Len(t, desc, 1)
	return desc[0].State
}

func waitGroupState(t *testing.T, broker, group string, want func(string) bool, what string) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	var state string
	for time.Now().Before(deadline) {
		if state = groupState(t, broker, group); want(state) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("group %s on %s is %q, never %s", group, broker, state, what)
}

type idleHandler struct{}

func (idleHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (idleHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (idleHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-sess.Context().Done():
			return nil
		case _, ok := <-claim.Messages():
			if !ok {
				return nil
			}
		}
	}
}

// joinGroup keeps a member of group consuming topic on the cluster at broker
// until the test ends, then waits for the group to empty again.
func joinGroup(t *testing.T, broker, group, topic string) {
	t.Helper()
	cfg := saramaConfig()
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cg, err := sarama.NewConsumerGroup([]string{broker}, group, cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			_ = cg.Consume(ctx, []string{topic}, idleHandler{})
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = cg.Close()
		waitGroupState(t, broker, group, func(s string) bool { return s != "Stable" && s != "PreparingRebalance" && s != "CompletingRebalance" }, "idle")
	})
	waitGroupState(t, broker, group, func(s string) bool { return s == "Stable" }, "Stable")
}

// commitOffLink creates topic on the source with one record, commits group on
// it, and registers the deletion of both as cleanup.
func commitOffLink(t *testing.T, topic, group string) {
	t.Helper()
	cfg := saramaConfig()
	cfg.Producer.Return.Successes = true
	client, err := sarama.NewClient([]string{sourceBroker}, cfg)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	admin, err := sarama.NewClusterAdminFromClient(client)
	require.NoError(t, err)

	require.NoError(t, admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false))
	t.Cleanup(func() {
		a, err := sarama.NewClusterAdmin([]string{sourceBroker}, saramaConfig())
		require.NoError(t, err)
		defer func() { _ = a.Close() }()
		require.NoError(t, a.DeleteConsumerGroup(group))
		require.NoError(t, a.DeleteTopic(topic))
	})

	producer, err := sarama.NewSyncProducerFromClient(client)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Value: sarama.StringEncoder("x")})
		return err == nil
	}, settleTimeout, 500*time.Millisecond, "producing to %s", topic)

	om, err := sarama.NewOffsetManagerFromClient(group, client)
	require.NoError(t, err)
	pom, err := om.ManagePartition(topic, 0)
	require.NoError(t, err)
	pom.MarkOffset(1, "")
	om.Commit()
	require.NoError(t, pom.Close())
	require.NoError(t, om.Close())

	require.Eventually(t, func() bool {
		offsets, err := admin.ListConsumerGroupOffsets(group, map[string][]int32{topic: {0}})
		if err != nil {
			return false
		}
		b := offsets.GetBlock(topic, 0)
		return b != nil && b.Offset == 1
	}, settleTimeout, 500*time.Millisecond, "group %s never committed on %s", group, topic)
}
