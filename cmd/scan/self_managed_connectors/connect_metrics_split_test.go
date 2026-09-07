package self_managed_connectors

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func f(v float64) *float64 { return &v }

// TestSplitConnectMetrics_RoutesResultsAndAggregates verifies that composite
// "<metric> (<connector>)" labels on Results and Aggregates are stripped and
// routed to a per-connector block, while bare cluster labels stay on the
// cluster block, and that Metadata is copied into every block.
func TestSplitConnectMetrics_RoutesResultsAndAggregates(t *testing.T) {
	cm := &types.ConnectClusterMetrics{
		Metadata: types.ConnectMetricMetadata{Period: 10, MetricsSource: types.MetricBackendJolokia},
		Metrics: []types.ProcessedMetric{
			{Label: "task-count", Value: f(4)},
			{Label: "source-record-write-rate (c1)", Value: f(1)},
			{Label: "source-record-write-rate (c2)", Value: f(2)},
		},
		Aggregates: map[string]types.MetricAggregate{
			"task-count":                    {Average: f(4)},
			"source-record-write-rate (c1)": {Average: f(1)},
		},
	}

	cluster, per := splitConnectMetrics(cm)

	// cluster keeps only non-connector metrics, bare labels
	require.Len(t, cluster.Metrics, 1)
	assert.Equal(t, "task-count", cluster.Metrics[0].Label)
	require.Len(t, cluster.Aggregates, 1)
	_, ok := cluster.Aggregates["task-count"]
	assert.True(t, ok)

	// per-connector: bare labels, keyed by connector
	require.Len(t, per, 2)
	require.NotNil(t, per["c1"])
	require.NotNil(t, per["c2"])

	require.Len(t, per["c1"].Metrics, 1)
	assert.Equal(t, "source-record-write-rate", per["c1"].Metrics[0].Label)
	_, ok = per["c1"].Aggregates["source-record-write-rate"]
	assert.True(t, ok, "c1 aggregate not stripped/routed")
	assert.Equal(t, int32(10), per["c1"].Metadata.Period, "metadata not copied to connector metrics")

	// c2 only appears in Results (no aggregate for it in this fixture)
	require.Len(t, per["c2"].Metrics, 1)
	assert.Equal(t, "source-record-write-rate", per["c2"].Metrics[0].Label)
	assert.Empty(t, per["c2"].Aggregates)
}

// TestSplitConnectMetrics_QueryInfoRouting is the corrected-behavior anchor:
// QueryInfo entries carry BARE metric names (collectors emit one query_info
// per metric describing the wildcard collection method, never a composite
// "<metric> (<connector>)" name). A query_info whose bare name matches a
// metric that was routed to one or more connectors must be copied into every
// connector block that actually has that metric, and must NOT also land on
// the cluster block. A query_info for a cluster-only metric stays on the
// cluster block only.
func TestSplitConnectMetrics_QueryInfoRouting(t *testing.T) {
	cm := &types.ConnectClusterMetrics{
		Metrics: []types.ProcessedMetric{
			{Label: "task-count", Value: f(4)},
			{Label: "source-record-write-rate (c1)", Value: f(1)},
			{Label: "source-record-write-rate (c2)", Value: f(2)},
		},
		QueryInfo: []types.MetricQueryInfo{
			{MetricName: "task-count"},
			{MetricName: "source-record-write-rate"},
		},
	}

	cluster, per := splitConnectMetrics(cm)

	// cluster metric's query_info lands on the cluster block only.
	require.Len(t, cluster.QueryInfo, 1)
	assert.Equal(t, "task-count", cluster.QueryInfo[0].MetricName)

	// per-connector metric's query_info is copied into every connector that
	// has the metric, not left on the cluster block.
	for _, name := range cluster.QueryInfo {
		assert.NotEqual(t, "source-record-write-rate", name.MetricName, "per-connector query_info must not leak onto the cluster block")
	}
	require.Len(t, per["c1"].QueryInfo, 1)
	assert.Equal(t, "source-record-write-rate", per["c1"].QueryInfo[0].MetricName)
	require.Len(t, per["c2"].QueryInfo, 1)
	assert.Equal(t, "source-record-write-rate", per["c2"].QueryInfo[0].MetricName)
}

// TestSplitConnectMetrics_QueryInfoOnlyCopiedWhereMetricPresent guards against
// a naive "copy into every connector block regardless" implementation: c3 has
// no source-record-write-rate metric at all (a different per-connector metric
// put it in the map), so it must not receive that query_info.
func TestSplitConnectMetrics_QueryInfoOnlyCopiedWhereMetricPresent(t *testing.T) {
	cm := &types.ConnectClusterMetrics{
		Metrics: []types.ProcessedMetric{
			{Label: "source-record-write-rate (c1)", Value: f(1)},
			{Label: "sink-record-read-rate (c3)", Value: f(3)},
		},
		QueryInfo: []types.MetricQueryInfo{
			{MetricName: "source-record-write-rate"},
			{MetricName: "sink-record-read-rate"},
		},
	}

	_, per := splitConnectMetrics(cm)

	require.NotNil(t, per["c3"])
	require.Len(t, per["c3"].QueryInfo, 1)
	assert.Equal(t, "sink-record-read-rate", per["c3"].QueryInfo[0].MetricName)
}

func TestParseConnectorLabel(t *testing.T) {
	tests := []struct {
		name         string
		label        string
		wantMetric   string
		wantConn     string
		wantOK       bool
		wantUnparsed string
	}{
		{name: "bare cluster label", label: "task-count", wantOK: false},
		{name: "composite label", label: "source-record-write-rate (c1)", wantMetric: "source-record-write-rate", wantConn: "c1", wantOK: true},
		{name: "connector name with spaces", label: "source-record-write-rate (my connector)", wantMetric: "source-record-write-rate", wantConn: "my connector", wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metric, connector, ok := parseConnectorLabel(tt.label)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantMetric, metric)
				assert.Equal(t, tt.wantConn, connector)
			} else {
				assert.Equal(t, tt.label, metric)
			}
		})
	}
}

func TestSplitConnectMetrics_Nil(t *testing.T) {
	cluster, per := splitConnectMetrics(nil)
	assert.Nil(t, cluster)
	assert.Nil(t, per)
}

// TestUpdateStateWithConnectMetrics_RoutesClusterAndConnector proves the
// scanner wiring: a full metrics update splits collected metrics into the
// cluster-level block and the matching Connector's own Metrics.
func TestUpdateStateWithConnectMetrics_RoutesClusterAndConnector(t *testing.T) {
	st := &types.State{OSKSources: &types.OSKSourcesState{Clusters: []types.OSKDiscoveredCluster{{ID: "c1"}}}}
	s := &SelfManagedConnectorsScanner{State: st, SourceType: types.SourceTypeOSK, ClusterID: "c1", connectRestURL: "u1"}
	require.NoError(t, s.updateStateWithConnectors([]types.Connector{{Name: "c1conn"}}))

	cm := &types.ConnectClusterMetrics{
		Metrics: []types.ProcessedMetric{
			{Label: "task-count", Value: f(2)},
			{Label: "source-record-write-rate (c1conn)", Value: f(9)},
		},
	}
	require.NoError(t, s.updateStateWithConnectMetrics(cm))

	cl, err := st.GetOSKClusterByID("c1")
	require.NoError(t, err)
	cc := cl.KafkaAdminClientInformation.ConnectClusters[0]

	require.Len(t, cc.Metrics.Metrics, 1)
	assert.Equal(t, "task-count", cc.Metrics.Metrics[0].Label)

	require.NotNil(t, cc.Connectors[0].Metrics)
	require.Len(t, cc.Connectors[0].Metrics.Metrics, 1)
	assert.Equal(t, "source-record-write-rate", cc.Connectors[0].Metrics.Metrics[0].Label)
}
