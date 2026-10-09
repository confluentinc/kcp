//go:build e2e

package routeconversion

import (
	"crypto/tls"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/stretchr/testify/require"
)

// cluster picks the source (PLAINTEXT) or the destination (SASL_SSL PLAIN).
type cluster int

const (
	sourceCluster cluster = iota
	destCluster
)

func (c cluster) String() string {
	if c == sourceCluster {
		return "source"
	}
	return "destination"
}

func (e *env) bootstrap(c cluster) string {
	if c == sourceCluster {
		return e.sourceBootstrap
	}
	return e.destBootstrap
}

// groupClient is kcp's own consumer-group client for c, closed when the test ends.
func (e *env) groupClient(t *testing.T, c cluster) *client.ConsumerGroupClient {
	t.Helper()
	opt := client.WithUnauthenticatedPlaintextAuth()
	if c == destCluster {
		opt = client.WithSASLPlainAuth(e.saslUser, e.saslPassword, "", true)
	}
	gc, err := client.NewConsumerGroupClient([]string{e.bootstrap(c)}, "", opt)
	require.NoErrorf(t, err, "connect a consumer-group client to the %s", c)
	t.Cleanup(func() { _ = gc.Close() })
	return gc
}

// admin is a plain sarama admin for what kcp's client doesn't do: delete groups,
// read an offset's leader epoch, create a topic. Closed when the test ends.
func (e *env) admin(t *testing.T, c cluster) sarama.ClusterAdmin {
	t.Helper()
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_8_0_0
	cfg.ClientID = "kcp-route-conversion-e2e"
	if c == destCluster {
		cfg.Net.SASL.Enable = true
		cfg.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		cfg.Net.SASL.User = e.saslUser
		cfg.Net.SASL.Password = e.saslPassword
		cfg.Net.TLS.Enable = true
		// The destination's certificate is signed by setup's throwaway CA.
		cfg.Net.TLS.Config = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only cluster
	}
	a, err := sarama.NewClusterAdmin([]string{e.bootstrap(c)}, cfg)
	require.NoErrorf(t, err, "connect an admin client to the %s", c)
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// offsets is one group's committed offsets: topic -> partition -> offset.
type offsets map[string]map[int32]client.CommittedOffset

// seed is a consumer group whose offsets the harness commits on the source
// directly, with no live members.
type seed struct {
	group   string
	offsets offsets
}

// seedGroups commits each seed's offsets (with their metadata) on the source.
func (e *env) seedGroups(t *testing.T, seeds ...seed) []seed {
	t.Helper()
	gc := e.groupClient(t, sourceCluster)
	for _, s := range seeds {
		require.NoErrorf(t, gc.CommitGroupOffsets(s.group, s.offsets), "seed group %s on the source", s.group)
	}
	return seeds
}

// defaultSeeds are two groups on link topics: one on rc-topic-001's three
// partitions with distinct metadata strings, one on rc-topic-003. The offsets
// sit below the records setup.sh wrote to every partition.
func (e *env) defaultSeeds(prefix string) []seed {
	return []seed{
		{group: prefix + "-a", offsets: offsets{e.topic(1): {
			0: {Offset: 3, Metadata: prefix + "-meta-p0"},
			1: {Offset: 4, Metadata: prefix + "-meta-p1"},
			2: {Offset: 5, Metadata: prefix + "-meta-p2"},
		}}},
		{group: prefix + "-b", offsets: offsets{e.topic(3): {0: {Offset: 7}}}},
	}
}

// sourceOffsets reads one group's committed offsets on the source.
func (e *env) sourceOffsets(t *testing.T, group string) offsets {
	t.Helper()
	got, err := e.groupClient(t, sourceCluster).CommittedOffsets(group)
	require.NoErrorf(t, err, "read %s's offsets on the source", group)
	return got
}

// destOffset is one destination commit as an OffsetFetch v7 returns it.
type destOffset struct {
	Offset      int64
	Metadata    string
	LeaderEpoch int32
}

// destOffsets reads one group's committed offsets on the destination with
// their metadata and leader epoch (kcp's client drops the epoch).
func (e *env) destOffsets(t *testing.T, group string) map[string]map[int32]destOffset {
	t.Helper()
	resp, err := e.admin(t, destCluster).ListConsumerGroupOffsets(group, nil)
	require.NoErrorf(t, err, "read %s's offsets on the destination", group)
	out := map[string]map[int32]destOffset{}
	for topic, parts := range resp.Blocks {
		for p, b := range parts {
			require.Equalf(t, sarama.ErrNoError, b.Err, "%s %s[%d] on the destination", group, topic, p)
			if b.Offset < 0 {
				continue
			}
			if out[topic] == nil {
				out[topic] = map[int32]destOffset{}
			}
			out[topic][p] = destOffset{Offset: b.Offset, Metadata: b.Metadata, LeaderEpoch: b.LeaderEpoch}
		}
	}
	return out
}

// requireSynced asserts every seed's destination offsets equal its source
// offsets, metadata included, each written with leader epoch -1.
func (e *env) requireSynced(t *testing.T, seeds []seed) {
	t.Helper()
	for _, s := range seeds {
		src := e.sourceOffsets(t, s.group)
		dst := e.destOffsets(t, s.group)
		require.NotEmptyf(t, src, "%s must still have offsets on the source", s.group)
		require.Lenf(t, dst, len(src), "%s: the destination must hold exactly the source's topics", s.group)
		for topic, parts := range src {
			require.Lenf(t, dst[topic], len(parts), "%s %s: the destination must hold every source partition", s.group, topic)
			for p, c := range parts {
				d := dst[topic][p]
				require.Equalf(t, c.Offset, d.Offset, "%s %s[%d] offset", s.group, topic, p)
				require.Equalf(t, c.Metadata, d.Metadata, "%s %s[%d] metadata", s.group, topic, p)
				require.Equalf(t, int32(-1), d.LeaderEpoch, "%s %s[%d] leader epoch", s.group, topic, p)
			}
		}
	}
}

// requireNoDestOffsets asserts none of the seeds has offsets on the destination.
func (e *env) requireNoDestOffsets(t *testing.T, seeds []seed, why string) {
	t.Helper()
	for _, s := range seeds {
		require.Emptyf(t, e.destOffsets(t, s.group), "%s: %s must have no offsets on the destination", why, s.group)
	}
}

// deleteAllGroups deletes every consumer group on c. A group whose members are
// still leaving is retried until the deadline.
func (e *env) deleteAllGroups(t *testing.T, c cluster) {
	t.Helper()
	listing, err := e.groupClient(t, c).ListGroupsAllBrokers()
	require.NoErrorf(t, err, "list the %s's groups", c)
	admin := e.admin(t, c)
	deadline := time.Now().Add(90 * time.Second)
	for _, l := range listing {
		for {
			err := admin.DeleteConsumerGroup(l.GroupID)
			if err == nil || errors.Is(err, sarama.ErrGroupIDNotFound) {
				break
			}
			if errors.Is(err, sarama.ErrNonEmptyGroup) && time.Now().Before(deadline) {
				time.Sleep(2 * time.Second)
				continue
			}
			require.NoErrorf(t, err, "delete group %s on the %s", l.GroupID, c)
		}
	}
	after, err := e.groupClient(t, c).ListGroupsAllBrokers()
	require.NoError(t, err)
	var left []string
	for _, l := range after {
		left = append(left, l.GroupID)
	}
	sort.Strings(left)
	require.Emptyf(t, left, "every group on the %s must be gone", c)
}

// waitGroupState waits until group on c has the given state (case-insensitive)
// and at least members members.
func (e *env) waitGroupState(t *testing.T, c cluster, group, state string, members int, timeout time.Duration) {
	t.Helper()
	gc := e.groupClient(t, c)
	var lastErr error
	deadline := time.Now().Add(timeout)
	for {
		ds, err := gc.DescribeGroups([]string{group})
		switch {
		case err != nil:
			lastErr = err
			t.Logf("poll (group %s): transient describe error, retrying: %v", group, err)
		case len(ds) == 1 && strings.EqualFold(ds[0].State, state) && len(ds[0].Members) >= members:
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("group %s on the %s must reach %s with %d member(s) within %s; last read error: %v", group, c, state, members, timeout, lastErr)
		}
		time.Sleep(time.Second)
	}
}
