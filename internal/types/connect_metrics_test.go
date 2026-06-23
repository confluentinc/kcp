package types

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleConnectClusterMetrics builds a fully-populated value for serialization tests.
func sampleConnectClusterMetrics() ConnectClusterMetrics {
	start := time.Date(2026, 6, 16, 14, 54, 3, 0, time.UTC)
	end := time.Date(2026, 6, 23, 14, 54, 3, 0, time.UTC)
	v := 4.0
	return ConnectClusterMetrics{
		Metadata: ConnectMetricMetadata{
			StartDate:     start,
			EndDate:       end,
			Period:        60,
			MetricsSource: "prometheus",
		},
		Metrics: []ProcessedMetric{
			{Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339), Label: "connector-count", Value: &v},
		},
		Aggregates: map[string]MetricAggregate{
			"connector-count": {Average: &v, Maximum: &v, Minimum: &v},
		},
		QueryInfo: []MetricQueryInfo{
			{MetricName: "connector-count", SourceType: MetricBackendPrometheus},
		},
	}
}

// TestConnectClusterMetrics_JSONShape asserts the envelope and metadata serialize
// with exactly the Connect-meaningful keys — no broker fields, no region/cluster_arn.
func TestConnectClusterMetrics_JSONShape(t *testing.T) {
	b, err := json.Marshal(sampleConnectClusterMetrics())
	require.NoError(t, err)

	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &envelope))

	// Envelope keys exactly.
	envelopeKeys := keysOf(envelope)
	assert.ElementsMatch(t, []string{"metadata", "results", "aggregates", "query_info"}, envelopeKeys,
		"envelope must carry only metadata/results/aggregates/query_info, got %v", envelopeKeys)

	// Top-level broker-envelope noise must be gone.
	assert.NotContains(t, envelopeKeys, "region")
	assert.NotContains(t, envelopeKeys, "cluster_arn")

	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["metadata"], &meta))
	metaKeys := keysOf(meta)
	assert.ElementsMatch(t, []string{"start_date", "end_date", "period", "metrics_source"}, metaKeys,
		"metadata must carry only start_date/end_date/period/metrics_source, got %v", metaKeys)

	// None of the broker metadata fields may appear.
	for _, brokerField := range []string{
		"cluster_type", "number_of_broker_nodes", "kafka_version", "broker_az_distribution",
		"enhanced_monitoring", "follower_fetching", "instance_type", "tiered_storage", "broker_type",
	} {
		assert.NotContains(t, metaKeys, brokerField, "broker field %q must not serialize for Connect", brokerField)
	}
}

// TestConnectClusterMetrics_RoundTrip confirms marshal->unmarshal preserves the data.
func TestConnectClusterMetrics_RoundTrip(t *testing.T) {
	original := sampleConnectClusterMetrics()
	b, err := json.Marshal(original)
	require.NoError(t, err)

	var got ConnectClusterMetrics
	require.NoError(t, json.Unmarshal(b, &got))

	assert.True(t, original.Metadata.StartDate.Equal(got.Metadata.StartDate))
	assert.True(t, original.Metadata.EndDate.Equal(got.Metadata.EndDate))
	assert.Equal(t, int32(60), got.Metadata.Period)
	assert.Equal(t, "prometheus", got.Metadata.MetricsSource)
	require.Len(t, got.Metrics, 1)
	assert.Equal(t, "connector-count", got.Metrics[0].Label)
	require.NotNil(t, got.Metrics[0].Value)
	assert.Equal(t, 4.0, *got.Metrics[0].Value)
}

// TestConnectMetricMetadata_MetricsSourceAlwaysPresent locks the gate decision:
// metrics_source has no omitempty, so an unset source still serializes the key.
func TestConnectMetricMetadata_MetricsSourceAlwaysPresent(t *testing.T) {
	b, err := json.Marshal(ConnectMetricMetadata{})
	require.NoError(t, err)

	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &meta))
	assert.Contains(t, keysOf(meta), "metrics_source",
		"metrics_source must always serialize (no omitempty)")
	assert.JSONEq(t, `""`, string(meta["metrics_source"]))
}

// TestConnectClusterMetrics_BackwardCompatUnmarshal is the abuse/edge case (R4):
// a legacy state file whose Connect metrics carry the old broker-shaped fields must
// load without error. Unknown broker fields are ignored; start/end/period/results
// populate; metrics_source defaults to empty.
func TestConnectClusterMetrics_BackwardCompatUnmarshal(t *testing.T) {
	legacy := `{
		"region": "us-east-1",
		"cluster_arn": "arn:aws:kafka:us-east-1:123:cluster/foo/abc",
		"metadata": {
			"cluster_type": "PROVISIONED",
			"number_of_broker_nodes": 3,
			"kafka_version": "3.5.1",
			"broker_az_distribution": "DEFAULT",
			"enhanced_monitoring": "PER_BROKER",
			"start_date": "2026-06-16T14:54:03Z",
			"end_date": "2026-06-23T14:54:03Z",
			"period": 300,
			"follower_fetching": false,
			"instance_type": "kafka.m5.large",
			"tiered_storage": false,
			"broker_type": "standard"
		},
		"results": [
			{"start": "2026-06-16T14:54:03Z", "end": "2026-06-16T14:55:03Z", "label": "connector-count", "value": 4}
		],
		"aggregates": {"connector-count": {"avg": 4, "max": 4, "min": 4}},
		"query_info": []
	}`

	var got ConnectClusterMetrics
	err := json.Unmarshal([]byte(legacy), &got)
	require.NoError(t, err, "legacy broker-shaped Connect metrics must unmarshal without error")

	// Meaningful fields survive.
	assert.Equal(t, time.Date(2026, 6, 16, 14, 54, 3, 0, time.UTC), got.Metadata.StartDate.UTC())
	assert.Equal(t, time.Date(2026, 6, 23, 14, 54, 3, 0, time.UTC), got.Metadata.EndDate.UTC())
	assert.Equal(t, int32(300), got.Metadata.Period)
	// metrics_source absent from legacy data -> empty.
	assert.Equal(t, "", got.Metadata.MetricsSource)
	// Metric series preserved.
	require.Len(t, got.Metrics, 1)
	assert.Equal(t, "connector-count", got.Metrics[0].Label)
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
