//go:build e2e

package routeconversion

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/stretchr/testify/require"
)

// linkConfig is the destination cluster-link REST config; the SASL credential
// (pod-spec env) is the HTTP basic auth, as in setup.sh.
func (e *env) linkConfig() clusterlink.Config {
	return clusterlink.Config{RestEndpoint: e.restEndpoint, ClusterID: e.destClusterID, LinkName: e.linkName,
		APIKey: e.saslUser, APISecret: e.saslPassword}
}

// mirrors reads every mirror topic on the link.
func (e *env) mirrors(t *testing.T, ctx context.Context) []clusterlink.MirrorTopic {
	t.Helper()
	ms, err := e.linkSvc.ListMirrorTopics(ctx, e.linkConfig())
	require.NoError(t, err, "list the link's mirror topics")
	return ms
}

// mirrorStates is each mirror's status, by source topic.
func (e *env) mirrorStates(t *testing.T, ctx context.Context) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range e.mirrors(t, ctx) {
		out[m.SourceTopicName] = m.MirrorStatus
	}
	return out
}

// linkTopics is every topic on the link, sorted: the topics a conversion is
// scoped to.
func (e *env) linkTopics(t *testing.T, ctx context.Context) []string {
	t.Helper()
	var out []string
	for name := range e.mirrorStates(t, ctx) {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// promoteAll promotes every link mirror that is not STOPPED and waits until
// all are, so the link is fully promoted again.
func (e *env) promoteAll(t *testing.T, ctx context.Context) {
	t.Helper()
	var pending []string
	for name, status := range e.mirrorStates(t, ctx) {
		if status != clusterlink.MirrorStatusStopped && status != clusterlink.MirrorStatusPendingStopped {
			pending = append(pending, name)
		}
	}
	if len(pending) > 0 {
		sort.Strings(pending)
		resp, err := e.linkSvc.PromoteMirrorTopics(ctx, e.linkConfig(), pending)
		require.NoError(t, err, "promote %v", pending)
		for _, d := range resp.Data {
			require.Zerof(t, d.ErrorCode, "promote %s: %s", d.MirrorTopicName, d.ErrorMessage)
		}
		t.Logf("promoted %v", pending)
	}
	require.Eventually(t, func() bool {
		for _, status := range e.mirrorStates(t, ctx) {
			if status != clusterlink.MirrorStatusStopped {
				return false
			}
		}
		return true
	}, 2*time.Minute, 2*time.Second, "every link mirror must reach STOPPED")
}

// addMirror creates topic on the source (1 partition) and mirrors it on the
// link, then waits for the mirror to be ACTIVE. clusterlink.CreateMirrorTopic
// is the REST call; the topic is created over the Kafka protocol first.
func (e *env) addMirror(t *testing.T, ctx context.Context, topic string) {
	t.Helper()
	admin := e.admin(t, sourceCluster)
	err := admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false)
	require.NoError(t, err, "create %s on the source", topic)
	require.NoError(t, e.linkSvc.CreateMirrorTopic(ctx, e.linkConfig(), topic, topic), "mirror %s on the link", topic)
	require.Eventually(t, func() bool {
		return e.mirrorStates(t, ctx)[topic] == clusterlink.MirrorStatusActive
	}, 2*time.Minute, 2*time.Second, "the new mirror %s must become ACTIVE", topic)
}
