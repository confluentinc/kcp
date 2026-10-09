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

// One-shot mirror reads (mirrors, and mirrorStates, linkTopics, promoteAll and
// snapshot through it) retry a failed read this many times, this far apart, so
// a transient link REST 500 does not fail the test.
const (
	mirrorReadAttempts = 5
	mirrorReadBackoff  = 2 * time.Second
)

// mirrors reads every mirror topic on the link, retrying a failed read a few
// times (the link REST API answers a transient 500 now and then) and failing
// with the last error only after that.
func (e *env) mirrors(t *testing.T, ctx context.Context) []clusterlink.MirrorTopic {
	t.Helper()
	var err error
	for attempt := 1; attempt <= mirrorReadAttempts; attempt++ {
		var ms []clusterlink.MirrorTopic
		if ms, err = e.linkSvc.ListMirrorTopics(ctx, e.linkConfig()); err == nil {
			return ms
		}
		if attempt < mirrorReadAttempts {
			t.Logf("list the link's mirror topics (attempt %d/%d) failed, retrying in %s: %v", attempt, mirrorReadAttempts, mirrorReadBackoff, err)
			time.Sleep(mirrorReadBackoff)
		}
	}
	require.NoErrorf(t, err, "list the link's mirror topics (%d attempts)", mirrorReadAttempts)
	return nil
}

// tryMirrorStates is mirrorStates without the assertion, for use inside polls:
// the link REST API answers a transient 500 ("Replica status is temporarily
// unavailable") now and then, which a poll must ride out.
func (e *env) tryMirrorStates(ctx context.Context) (map[string]string, error) {
	ms, err := e.linkSvc.ListMirrorTopics(ctx, e.linkConfig())
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, m := range ms {
		out[m.SourceTopicName] = m.MirrorStatus
	}
	return out, nil
}

// pollMirrors polls the link's mirror states every interval until done accepts
// them or timeout passes. A read error counts as "not yet" (logged); on timeout
// the failure names the last read error, if any.
func (e *env) pollMirrors(t *testing.T, ctx context.Context, timeout, interval time.Duration, done func(map[string]string) bool, what string) {
	t.Helper()
	var lastErr error
	var last map[string]string
	deadline := time.Now().Add(timeout)
	for {
		states, err := e.tryMirrorStates(ctx)
		if err != nil {
			lastErr = err
			t.Logf("poll (%s): transient link read error, retrying: %v", what, err)
		} else {
			last = states
			if done(states) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached within %s; last mirror states %v; last read error: %v", what, timeout, last, lastErr)
		}
		time.Sleep(interval)
	}
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
	e.pollMirrors(t, ctx, 2*time.Minute, 2*time.Second, func(states map[string]string) bool {
		for _, status := range states {
			if status != clusterlink.MirrorStatusStopped {
				return false
			}
		}
		return true
	}, "every link mirror must reach STOPPED")
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
	e.pollMirrors(t, ctx, 2*time.Minute, 2*time.Second, func(states map[string]string) bool {
		return states[topic] == clusterlink.MirrorStatusActive
	}, "the new mirror "+topic+" must become ACTIVE")
}
