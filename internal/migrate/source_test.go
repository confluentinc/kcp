package migrate

import (
	"context"
	"fmt"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/mocks"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// withMockAdmin swaps buildSourceAdmin for one returning the given mock, and
// restores it on cleanup.
func withMockAdmin(t *testing.T, m *mocks.MockKafkaAdmin) {
	t.Helper()
	old := buildSourceAdmin
	buildSourceAdmin = func(types.KafkaSourceConn) (client.KafkaAdmin, error) { return m, nil }
	t.Cleanup(func() { buildSourceAdmin = old })
}

func strPtr(s string) *string { return &s }

func TestKafkaSourceReader_ListTopics_SortsNames(t *testing.T) {
	withMockAdmin(t, &mocks.MockKafkaAdmin{
		ListTopicsWithConfigsFunc: func() (map[string]sarama.TopicDetail, error) {
			return map[string]sarama.TopicDetail{
				"zeta":  {},
				"alpha": {},
				"mike":  {},
			}, nil
		},
		CloseFunc: func() error { return nil },
	})

	r := NewKafkaSourceReader(types.KafkaSourceConn{})
	got, err := r.ListTopics(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "mike", "zeta"}, got)
}

func TestKafkaSourceReader_ClusterID_CachedAcrossCalls(t *testing.T) {
	// Count admin connections opened. The reader must fetch the (immutable)
	// cluster id once and memoize it, not re-connect on every ClusterID call.
	var conns, metaReads int
	old := buildSourceAdmin
	buildSourceAdmin = func(types.KafkaSourceConn) (client.KafkaAdmin, error) {
		conns++
		return &mocks.MockKafkaAdmin{
			GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
				metaReads++
				return &client.ClusterKafkaMetadata{ClusterID: "src-cluster-1"}, nil
			},
			CloseFunc: func() error { return nil },
		}, nil
	}
	t.Cleanup(func() { buildSourceAdmin = old })

	r := NewKafkaSourceReader(types.KafkaSourceConn{})
	// Mirrors the clusterLink reconciler: CheckPreconditions probes, Plan reads.
	id1, err := r.ClusterID(context.Background())
	require.NoError(t, err)
	id2, err := r.ClusterID(context.Background())
	require.NoError(t, err)

	require.Equal(t, "src-cluster-1", id1)
	require.Equal(t, id1, id2)
	require.Equal(t, 1, conns, "ClusterID must open only one admin connection across calls")
	require.Equal(t, 1, metaReads, "cluster metadata must be read only once")
}

func TestKafkaSourceReader_ClusterID_ErrorNotCached(t *testing.T) {
	// A failed read must NOT be cached — the next call retries (and can succeed).
	var attempts int
	old := buildSourceAdmin
	buildSourceAdmin = func(types.KafkaSourceConn) (client.KafkaAdmin, error) {
		attempts++
		first := attempts == 1
		return &mocks.MockKafkaAdmin{
			GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
				if first {
					return nil, fmt.Errorf("boom")
				}
				return &client.ClusterKafkaMetadata{ClusterID: "src-cluster-1"}, nil
			},
			CloseFunc: func() error { return nil },
		}, nil
	}
	t.Cleanup(func() { buildSourceAdmin = old })

	r := NewKafkaSourceReader(types.KafkaSourceConn{})
	_, err := r.ClusterID(context.Background())
	require.Error(t, err)
	id, err := r.ClusterID(context.Background())
	require.NoError(t, err)
	require.Equal(t, "src-cluster-1", id)
	require.Equal(t, 2, attempts, "a failed read must retry, not serve a cached error")
}

func TestKafkaSourceReader_DescribeTopics_FiltersAndMaps(t *testing.T) {
	withMockAdmin(t, &mocks.MockKafkaAdmin{
		ListTopicsWithNonDefaultConfigsFunc: func() (map[string]sarama.TopicDetail, error) {
			return map[string]sarama.TopicDetail{
				"orders": {
					NumPartitions:     6,
					ReplicationFactor: 3,
					ConfigEntries: map[string]*string{
						"retention.ms":   strPtr("604800000"),
						"cleanup.policy": strPtr("compact"),
						"nilval":         nil, // must be dropped
					},
				},
				"events": {NumPartitions: 1, ReplicationFactor: 1},
				// not requested → must be excluded
				"internal": {NumPartitions: 50, ReplicationFactor: 3},
			}, nil
		},
		CloseFunc: func() error { return nil },
	})

	r := NewKafkaSourceReader(types.KafkaSourceConn{})
	got, err := r.DescribeTopics(context.Background(), []string{"orders", "events", "absent"})
	require.NoError(t, err)

	// sorted by name; "absent" is silently skipped; "internal" excluded
	require.Len(t, got, 2)
	require.Equal(t, "events", got[0].Name)
	require.Equal(t, "orders", got[1].Name)

	orders := got[1]
	require.Equal(t, 6, orders.Partitions)
	require.Equal(t, 3, orders.ReplicationFactor)
	require.Equal(t, map[string]string{
		"retention.ms":   "604800000",
		"cleanup.policy": "compact",
	}, orders.Configs)
}
