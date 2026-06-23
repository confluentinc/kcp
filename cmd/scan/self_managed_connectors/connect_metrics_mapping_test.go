package self_managed_connectors

import (
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// U3: the boundary mapper converts the shared collector output
// (*types.ProcessedClusterMetrics) into the Connect-specific
// *types.ConnectClusterMetrics, injecting the backend that produced it and
// carrying the consumable fields through unchanged.
func TestToConnectClusterMetrics_InjectsSourceAndCopiesFields(t *testing.T) {
	start := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC)
	v := 4.0
	pcm := &types.ProcessedClusterMetrics{
		// Broker-only fields are present on the source but must be dropped.
		Region:     "us-east-1",
		ClusterArn: "arn:aws:kafka:...",
		Metadata: types.MetricMetadata{
			StartDate:           start,
			EndDate:             end,
			Period:              60,
			NumberOfBrokerNodes: 3,
			KafkaVersion:        "3.5.1",
		},
		Metrics:    []types.ProcessedMetric{{Label: "connector-count", Value: &v}},
		Aggregates: map[string]types.MetricAggregate{"connector-count": {Average: &v}},
		QueryInfo:  []types.MetricQueryInfo{{MetricName: "connector-count"}},
	}

	got := toConnectClusterMetrics(pcm, types.MetricBackendPrometheus)

	require.NotNil(t, got)
	assert.Equal(t, types.MetricBackendPrometheus, got.Metadata.MetricsSource, "backend must be injected")
	assert.True(t, start.Equal(got.Metadata.StartDate))
	assert.True(t, end.Equal(got.Metadata.EndDate))
	assert.Equal(t, int32(60), got.Metadata.Period)
	assert.Equal(t, pcm.Metrics, got.Metrics, "metric series carried through")
	assert.Equal(t, pcm.Aggregates, got.Aggregates, "aggregates carried through")
	assert.Equal(t, pcm.QueryInfo, got.QueryInfo, "query info carried through")
}

// Edge: a nil collector result maps to nil (the collect path returns an error
// before mapping, but the mapper must not panic if handed nil).
func TestToConnectClusterMetrics_Nil(t *testing.T) {
	assert.Nil(t, toConnectClusterMetrics(nil, types.MetricBackendJolokia))
}
