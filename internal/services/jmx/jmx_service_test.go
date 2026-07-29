package jmx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockJolokiaServer(t *testing.T) *httptest.Server {
	t.Helper()
	var callCount atomic.Int64

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		var response map[string]any

		switch {
		case strings.Contains(r.URL.Path, "BytesInPerSec"):
			response = map[string]any{"status": 200, "value": map[string]any{"Count": float64(n * 1000)}}
		case strings.Contains(r.URL.Path, "BytesOutPerSec"):
			response = map[string]any{"status": 200, "value": map[string]any{"Count": float64(n * 500)}}
		case strings.Contains(r.URL.Path, "MessagesInPerSec"):
			response = map[string]any{"status": 200, "value": map[string]any{"Count": float64(n * 10)}}
		case strings.Contains(r.URL.Path, "GlobalPartitionCount"):
			response = map[string]any{"status": 200, "value": map[string]any{"Value": 20.0}}
		case strings.Contains(r.URL.Path, "PartitionCount"):
			response = map[string]any{"status": 200, "value": map[string]any{"Value": 50.0}}
		case strings.Contains(r.URL.Path, "socket-server-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"l1": map[string]any{"connection-count": 2.0},
				"l2": map[string]any{"connection-count": 1.0},
			}}
		case strings.Contains(r.URL.Path, "kafka.log"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"p0": map[string]any{"Value": 536870912.0},
				"p1": map[string]any{"Value": 536870912.0},
			}}
		default:
			response = map[string]any{"status": 404, "error": "MBean not found"}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
}

func TestComputeSnapshot_RatesFromCounterDeltas(t *testing.T) {
	prev := &rawSample{
		timestamp: time.Now(),
		counters:  map[string]float64{"BytesInPerSec": 10000, "BytesOutPerSec": 5000, "MessagesInPerSec": 100},
		gauges:    map[string]float64{"PartitionCount": 50, "GlobalPartitionCount": 20, "ClientConnectionCount": 3, "TotalLocalStorageUsage": 1073741824},
	}
	curr := &rawSample{
		timestamp: prev.timestamp.Add(10 * time.Second),
		counters:  map[string]float64{"BytesInPerSec": 20000, "BytesOutPerSec": 10000, "MessagesInPerSec": 200},
		gauges:    map[string]float64{"PartitionCount": 50, "GlobalPartitionCount": 20, "ClientConnectionCount": 5, "TotalLocalStorageUsage": 2147483648},
	}

	snapshot := computeSnapshot(prev, curr, BrokerMetricDefinitions().UnitConversions)

	assert.Equal(t, 1000.0, snapshot.metrics["BytesInPerSec"])
	assert.Equal(t, 500.0, snapshot.metrics["BytesOutPerSec"])
	assert.Equal(t, 10.0, snapshot.metrics["MessagesInPerSec"])
	assert.Equal(t, 50.0, snapshot.metrics["PartitionCount"])
	assert.Equal(t, 20.0, snapshot.metrics["GlobalPartitionCount"])
	assert.Equal(t, 5.0, snapshot.metrics["ClientConnectionCount"])
	assert.Equal(t, 2.0, snapshot.metrics["TotalLocalStorageUsage"])
	assert.Equal(t, prev.timestamp, snapshot.start)
	assert.Equal(t, curr.timestamp, snapshot.end)
}

func TestComputeSnapshot_CounterResetProducesZeroNotNegative(t *testing.T) {
	prev := &rawSample{
		timestamp: time.Now(),
		counters:  map[string]float64{"BytesInPerSec": 50000, "BytesOutPerSec": 20000, "MessagesInPerSec": 500},
		gauges:    map[string]float64{"PartitionCount": 50},
	}
	// Simulate broker restart: counters reset to values lower than previous
	curr := &rawSample{
		timestamp: prev.timestamp.Add(10 * time.Second),
		counters:  map[string]float64{"BytesInPerSec": 1000, "BytesOutPerSec": 500, "MessagesInPerSec": 10},
		gauges:    map[string]float64{"PartitionCount": 50},
	}

	snapshot := computeSnapshot(prev, curr, nil)

	// Counter metrics should be absent (skipped), not negative
	_, hasBytes := snapshot.metrics["BytesInPerSec"]
	_, hasBytesOut := snapshot.metrics["BytesOutPerSec"]
	_, hasMessages := snapshot.metrics["MessagesInPerSec"]
	assert.False(t, hasBytes, "BytesInPerSec should be skipped on counter reset")
	assert.False(t, hasBytesOut, "BytesOutPerSec should be skipped on counter reset")
	assert.False(t, hasMessages, "MessagesInPerSec should be skipped on counter reset")

	// Gauge metrics should still be present
	assert.Equal(t, 50.0, snapshot.metrics["PartitionCount"])
}

func TestToProcessedClusterMetrics(t *testing.T) {
	now := time.Now()
	snapshots := []jmxSnapshot{
		{
			start:   now,
			end:     now.Add(1 * time.Second),
			metrics: map[string]float64{"BytesInPerSec": 1000, "PartitionCount": 7},
		},
		{
			start:   now.Add(1 * time.Second),
			end:     now.Add(2 * time.Second),
			metrics: map[string]float64{"BytesInPerSec": 2000, "PartitionCount": 7},
		},
		{
			start:   now.Add(2 * time.Second),
			end:     now.Add(3 * time.Second),
			metrics: map[string]float64{"BytesInPerSec": 1500, "PartitionCount": 7},
		},
	}

	result := toProcessedClusterMetrics(snapshots, now, 3*time.Second, 1*time.Second)

	// 3 snapshots × 2 metrics = 6 ProcessedMetric rows
	assert.Len(t, result.Metrics, 6)

	// Check aggregates
	bytesAgg := result.Aggregates["BytesInPerSec"]
	assert.NotNil(t, bytesAgg.Minimum)
	assert.NotNil(t, bytesAgg.Maximum)
	assert.NotNil(t, bytesAgg.Average)
	assert.Equal(t, 1000.0, *bytesAgg.Minimum)
	assert.Equal(t, 2000.0, *bytesAgg.Maximum)
	assert.Equal(t, 1500.0, *bytesAgg.Average)

	partAgg := result.Aggregates["PartitionCount"]
	assert.Equal(t, 7.0, *partAgg.Minimum)
	assert.Equal(t, 7.0, *partAgg.Maximum)
	assert.Equal(t, 7.0, *partAgg.Average)

	// Check metadata
	assert.Equal(t, int32(1), result.Metadata.Period)
	assert.Equal(t, now, result.Metadata.StartDate)
}

func TestCollectOverDuration_ReturnsProcessedClusterMetrics(t *testing.T) {
	server := mockJolokiaServer(t)
	defer server.Close()

	svc := NewJMXService([]string{server.URL}, BrokerMetricDefinitions(), "broker")
	result, err := svc.CollectOverDuration(context.Background(), 3*time.Second, 1*time.Second)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have ProcessedMetric rows
	assert.NotEmpty(t, result.Metrics)

	// Should have aggregates for rate and gauge metrics
	assert.NotEmpty(t, result.Aggregates)
	assert.Contains(t, result.Aggregates, "BytesInPerSec")
	assert.Contains(t, result.Aggregates, "PartitionCount")

	// Each ProcessedMetric should have start, end, label, value
	for _, m := range result.Metrics {
		assert.NotEmpty(t, m.Start)
		assert.NotEmpty(t, m.End)
		assert.NotEmpty(t, m.Label)
		assert.NotNil(t, m.Value)
	}

	// Metadata should be populated
	assert.Equal(t, int32(1), result.Metadata.Period)
	assert.False(t, result.Metadata.StartDate.IsZero())
	assert.False(t, result.Metadata.EndDate.IsZero())
}

func TestCollectOverDuration_PopulatesQueryInfo(t *testing.T) {
	server := mockJolokiaServer(t)
	defer server.Close()

	svc := NewJMXService([]string{server.URL}, BrokerMetricDefinitions(), "broker")
	result, err := svc.CollectOverDuration(context.Background(), 3*time.Second, 1*time.Second)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.QueryInfo)

	defs := BrokerMetricDefinitions()
	expectedCount := len(defs.Counters) + len(defs.Gauges) + len(defs.Controller) + len(defs.Aggregates)
	assert.Len(t, result.QueryInfo, expectedCount)

	for _, qi := range result.QueryInfo {
		assert.Equal(t, types.MetricBackendJolokia, qi.SourceType)
		assert.NotEmpty(t, qi.MetricName)
		assert.NotEmpty(t, qi.MBeanPath)
		assert.NotEmpty(t, qi.JolokiaURL)
		assert.Contains(t, qi.CurlCommand, "curl")
		assert.Contains(t, qi.CurlCommand, server.URL)
		assert.NotEmpty(t, qi.AggregationNote)
	}

	// Verify specific metrics are present
	names := make(map[string]bool)
	for _, qi := range result.QueryInfo {
		names[qi.MetricName] = true
	}
	assert.True(t, names["BytesInPerSec"])
	assert.True(t, names["BytesOutPerSec"])
	assert.True(t, names["MessagesInPerSec"])
	assert.True(t, names["PartitionCount"])
	assert.True(t, names["ClientConnectionCount"])
	assert.True(t, names["TotalLocalStorageUsage"])
}

func TestBuildJMXQueryInfo(t *testing.T) {
	brokerURLs := []string{"http://broker1:8778/jolokia", "http://broker2:8778/jolokia"}
	defs := BrokerMetricDefinitions()
	infos := buildJMXQueryInfo(brokerURLs, 5*time.Minute, 10*time.Second, defs, "broker")

	expectedCount := len(defs.Counters) + len(defs.Gauges) + len(defs.Controller) + len(defs.Aggregates)
	assert.Len(t, infos, expectedCount)

	// All entries should have Statistic, Period, and QueryDuration
	for _, info := range infos {
		assert.NotEmpty(t, info.Statistic)
		assert.Equal(t, int32(10), info.Period)
		assert.Equal(t, "5m", info.QueryDuration)
	}

	// Counter metrics should reference the Count attribute in aggregation note
	for _, info := range infos[:3] {
		assert.Contains(t, info.AggregationNote, "Count")
		assert.Contains(t, info.AggregationNote, "2 broker(s)")
		assert.Contains(t, info.CurlCommand, "broker1:8778")
		assert.Contains(t, info.Statistic, "Rate")
	}

	// Gauge metric (PartitionCount) should reference the Value attribute
	assert.Contains(t, infos[3].AggregationNote, "Value")
	assert.Equal(t, "PartitionCount", infos[3].MetricName)

	// Controller metric (GlobalPartitionCount) should reference controller MBean
	assert.Equal(t, "GlobalPartitionCount", infos[4].MetricName)
	assert.Contains(t, infos[4].AggregationNote, "Controller")
	assert.Contains(t, infos[4].MBeanPath, "KafkaController")

	// Aggregate metrics should reference wildcard
	assert.Contains(t, infos[5].AggregationNote, "Wildcard")
	assert.Contains(t, infos[6].AggregationNote, "Wildcard")

	// Empty brokerURLs should return nil
	assert.Nil(t, buildJMXQueryInfo(nil, 5*time.Minute, 10*time.Second, defs, "broker"))
	assert.Nil(t, buildJMXQueryInfo([]string{}, 5*time.Minute, 10*time.Second, defs, "broker"))
}

// TestBuildJMXQueryInfo_PerConnectorAggregates guards against the Task 2.2 regression
// where source-record-write-rate, source-record-poll-rate, sink-record-read-rate, and
// sink-record-send-rate (moved into/added to defs.PerConnectorAggregates) silently lost
// their MetricQueryInfo (reproducibility metadata surfaced in the UI's query tab) because
// buildJMXQueryInfo only iterated Counters, Gauges, Controller, and Aggregates.
func TestBuildJMXQueryInfo_PerConnectorAggregates(t *testing.T) {
	workerURLs := []string{"http://worker1:8778/jolokia"}
	defs := ConnectMetricDefinitions()
	infos := buildJMXQueryInfo(workerURLs, 5*time.Minute, 10*time.Second, defs, "worker")

	expectedCount := len(defs.Gauges) + len(defs.Aggregates) + len(defs.PerConnectorAggregates)
	assert.Len(t, infos, expectedCount)

	byName := make(map[string]types.MetricQueryInfo)
	for _, info := range infos {
		byName[info.MetricName] = info
	}

	for _, name := range []string{
		"source-record-write-rate",
		"source-record-poll-rate",
		"sink-record-read-rate",
		"sink-record-send-rate",
	} {
		info, ok := byName[name]
		require.True(t, ok, "expected MetricQueryInfo for %s (per-connector aggregate)", name)
		assert.Equal(t, types.MetricBackendJolokia, info.SourceType)
		assert.NotEmpty(t, info.MBeanPath)
		assert.Contains(t, info.MBeanPath, "connector=*")
		assert.NotEmpty(t, info.JolokiaURL)
		assert.Contains(t, info.CurlCommand, "curl")
		assert.Contains(t, info.CurlCommand, "worker1:8778")
		// Per-connector aggregates are broken down by connector, not summed cluster-wide.
		assert.Contains(t, info.AggregationNote, "connector")
		assert.NotContains(t, info.Statistic, "Sum of")
		assert.Equal(t, int32(10), info.Period)
		assert.Equal(t, "5m", info.QueryDuration)
	}
}

func TestCollectOverDuration_ControllerMBeanGracefulOmission(t *testing.T) {
	// Mock server that rejects controller MBeans (simulates non-controller broker)
	var callCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		var response map[string]any

		switch {
		case strings.Contains(r.URL.Path, "BytesInPerSec"):
			response = map[string]any{"status": 200, "value": map[string]any{"Count": float64(n * 1000)}}
		case strings.Contains(r.URL.Path, "BytesOutPerSec"):
			response = map[string]any{"status": 200, "value": map[string]any{"Count": float64(n * 500)}}
		case strings.Contains(r.URL.Path, "MessagesInPerSec"):
			response = map[string]any{"status": 200, "value": map[string]any{"Count": float64(n * 10)}}
		case strings.Contains(r.URL.Path, "kafka.controller"):
			// Controller MBean not available on this broker
			response = map[string]any{"status": 404, "error": "javax.management.InstanceNotFoundException: kafka.controller:type=KafkaController,name=GlobalPartitionCount"}
		case strings.Contains(r.URL.Path, "PartitionCount"):
			response = map[string]any{"status": 200, "value": map[string]any{"Value": 50.0}}
		case strings.Contains(r.URL.Path, "socket-server-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"l1": map[string]any{"connection-count": 2.0},
			}}
		case strings.Contains(r.URL.Path, "kafka.log"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"p0": map[string]any{"Value": 536870912.0},
			}}
		default:
			response = map[string]any{"status": 404, "error": "MBean not found"}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Capture slog output to assert the controller-missing caveat is logged once
	// per scan, not once per poll (collectRawSample runs on every tick).
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	svc := NewJMXService([]string{server.URL}, BrokerMetricDefinitions(), "broker")
	result, err := svc.CollectOverDuration(context.Background(), 3*time.Second, 1*time.Second)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Other metrics should be collected
	assert.NotEmpty(t, result.Metrics)
	assert.Contains(t, result.Aggregates, "BytesInPerSec")
	assert.Contains(t, result.Aggregates, "PartitionCount")

	// GlobalPartitionCount should NOT be in the aggregates (controller MBean unavailable)
	_, hasGlobal := result.Aggregates["GlobalPartitionCount"]
	assert.False(t, hasGlobal, "GlobalPartitionCount should be omitted when controller MBean is not available")

	// The caveat must be deduped to a single line even though the controller
	// MBean is polled on every sample across the scan.
	warnCount := strings.Count(logBuf.String(), "Controller MBean not available")
	assert.Equal(t, 1, warnCount, "controller-missing caveat should be logged once per scan, not per poll")

	// But query info should still include it (shows what was attempted)
	queryNames := make(map[string]bool)
	for _, qi := range result.QueryInfo {
		queryNames[qi.MetricName] = true
	}
	assert.True(t, queryNames["GlobalPartitionCount"], "QueryInfo should still include GlobalPartitionCount")
}

// mockSourceOnlyConnectJolokiaServer simulates a source-only Connect cluster:
// sink-task-metrics MBeans don't exist (404 InstanceNotFoundException), while
// everything else responds normally. This is the "no sink connectors" case
// that used to flood the log with a Warn on every poll.
func mockSourceOnlyConnectJolokiaServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var response map[string]any

		switch {
		case strings.Contains(r.URL.Path, "connect-worker-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{"connector-count": 1.0, "task-count": 1.0}}
		case strings.Contains(r.URL.Path, "source-task-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"kafka.connect:type=source-task-metrics,connector=c1,task=0": map[string]any{"source-record-write-rate": 1.0, "source-record-poll-rate": 1.0},
			}}
		case strings.Contains(r.URL.Path, "sink-task-metrics"):
			// No sink connectors on this cluster — Jolokia reports the wildcard
			// pattern as not found.
			response = map[string]any{"status": 404, "error": "javax.management.InstanceNotFoundException: kafka.connect:type=sink-task-metrics,connector=*,task=*"}
		case strings.Contains(r.URL.Path, "connect-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"client1": map[string]any{"incoming-byte-rate": 100.0, "outgoing-byte-rate": 50.0, "connection-count": 1.0, "request-rate": 10.0},
			}}
		default:
			response = map[string]any{"status": 404, "error": "MBean not found"}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
}

// TestCollectRawSample_MissingSinkMBeanLogsDebugOnceNotWarn is a regression
// test for the noisy-warning fix (Workstream E): a source-only Connect
// cluster has no sink-task MBeans, so the sink-task-metrics wildcard read
// returns 404/InstanceNotFoundException on every poll. This is an
// expected/normal condition, not a failure, so collectRawSample must:
//  1. complete without error and without populating sink-record-* gauges,
//  2. log at Debug (not Warn) the one time it logs at all,
//  3. dedupe repeated polls to a single log line via warnedMetricIssue,
//     mirroring warnedControllerMissing.
func TestCollectRawSample_MissingSinkMBeanLogsDebugOnceNotWarn(t *testing.T) {
	server := mockSourceOnlyConnectJolokiaServer(t)
	defer server.Close()

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	svc := NewJMXService([]string{server.URL}, ConnectMetricDefinitions(), "worker")

	// Poll collectRawSample multiple times, as CollectOverDuration would on
	// every tick, to verify the dedupe holds across repeated polls.
	for i := 0; i < 3; i++ {
		sample, err := svc.collectRawSample(context.Background())
		require.NoError(t, err)
		require.NotNil(t, sample)

		// Sink metrics should simply be absent — not an error, not a zero value.
		_, hasSinkRead := sample.gauges["sink-record-read-rate (c1)"]
		assert.False(t, hasSinkRead)

		// Source metrics (which exist) should still be collected normally.
		assert.Equal(t, 1.0, sample.gauges["source-record-write-rate (c1)"])
	}

	logs := logBuf.String()
	assert.NotContains(t, logs, "level=WARN", "missing sink MBeans on a source-only cluster is expected/normal — must not be a Warn")
	// ConnectMetricDefinitions has two sink-task-metrics entries
	// (sink-record-read-rate, sink-record-send-rate), each a distinct metric
	// name — so each gets its own one-time Debug line (2 total), deduped
	// across all 3 polls rather than logged once per poll (which would be 6).
	assert.Equal(t, 2, strings.Count(logs, "Failed to read per-connector aggregate MBean"),
		"each distinct sink metric should be logged exactly once across all 3 polls, not once per poll")
	assert.Contains(t, logs, "level=DEBUG")
}

func TestCollectOverDuration_DurationMustExceedInterval(t *testing.T) {
	svc := NewJMXService([]string{"http://localhost:1"}, BrokerMetricDefinitions(), "broker")
	_, err := svc.CollectOverDuration(context.Background(), 5*time.Second, 5*time.Second)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be greater than")
}

func TestConnectMetricDefinitions(t *testing.T) {
	defs := ConnectMetricDefinitions()

	require.Len(t, defs.Gauges, 2)
	assert.Equal(t, "connector-count", defs.Gauges[0].Name)
	assert.Equal(t, "task-count", defs.Gauges[1].Name)

	assert.Empty(t, defs.Counters)
	assert.Empty(t, defs.Controller)
	assert.Nil(t, defs.UnitConversions)

	// Connect definitions should have cluster-wide aggregate metrics for client-level metrics only —
	// source/sink task metrics are now broken down per connector (see PerConnectorAggregates below).
	require.Len(t, defs.Aggregates, 4)
	aggNames := make([]string, len(defs.Aggregates))
	for i, a := range defs.Aggregates {
		aggNames[i] = a.Name
	}
	assert.Contains(t, aggNames, "incoming-byte-rate")
	assert.Contains(t, aggNames, "outgoing-byte-rate")
	assert.Contains(t, aggNames, "connection-count")
	assert.Contains(t, aggNames, "request-rate")
	assert.NotContains(t, aggNames, "source-record-write-rate")
	assert.NotContains(t, aggNames, "source-record-poll-rate")

	// Source-task and sink-task metrics should be broken down per connector.
	require.Len(t, defs.PerConnectorAggregates, 4)
	perConnectorNames := make([]string, len(defs.PerConnectorAggregates))
	for i, a := range defs.PerConnectorAggregates {
		perConnectorNames[i] = a.Name
	}
	assert.Contains(t, perConnectorNames, "source-record-write-rate")
	assert.Contains(t, perConnectorNames, "source-record-poll-rate")
	assert.Contains(t, perConnectorNames, "sink-record-read-rate")
	assert.Contains(t, perConnectorNames, "sink-record-send-rate")
}

// mockConnectJolokiaServer returns a Jolokia mock for Connect worker MBeans,
// with source-task-metrics and sink-task-metrics wildcard reads returning
// per-connector/per-task ObjectName maps so per-connector grouping can be verified.
func mockConnectJolokiaServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var response map[string]any

		switch {
		case strings.Contains(r.URL.Path, "connect-worker-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{"connector-count": 2.0, "task-count": 3.0}}
		case strings.Contains(r.URL.Path, "source-task-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"kafka.connect:type=source-task-metrics,connector=c1,task=0": map[string]any{"source-record-write-rate": 1.0, "source-record-poll-rate": 1.0},
				"kafka.connect:type=source-task-metrics,connector=c1,task=1": map[string]any{"source-record-write-rate": 2.0, "source-record-poll-rate": 2.0},
				"kafka.connect:type=source-task-metrics,connector=c2,task=0": map[string]any{"source-record-write-rate": 5.0, "source-record-poll-rate": 5.0},
			}}
		case strings.Contains(r.URL.Path, "sink-task-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"kafka.connect:type=sink-task-metrics,connector=c3,task=0": map[string]any{"sink-record-read-rate": 4.0, "sink-record-send-rate": 4.0},
			}}
		case strings.Contains(r.URL.Path, "connect-metrics"):
			response = map[string]any{"status": 200, "value": map[string]any{
				"client1": map[string]any{"incoming-byte-rate": 100.0, "outgoing-byte-rate": 50.0, "connection-count": 1.0, "request-rate": 10.0},
			}}
		default:
			response = map[string]any{"status": 404, "error": "MBean not found"}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
}

func TestCollectRawSample_PerConnectorAggregates(t *testing.T) {
	server := mockConnectJolokiaServer(t)
	defer server.Close()

	svc := NewJMXService([]string{server.URL}, ConnectMetricDefinitions(), "worker")
	sample, err := svc.collectRawSample(context.Background())

	require.NoError(t, err)
	require.NotNil(t, sample)

	// source-task-metrics: c1 has two tasks (1.0 + 2.0 = 3.0), c2 has one task (5.0).
	assert.Equal(t, 3.0, sample.gauges["source-record-write-rate (c1)"])
	assert.Equal(t, 5.0, sample.gauges["source-record-write-rate (c2)"])
	assert.Equal(t, 3.0, sample.gauges["source-record-poll-rate (c1)"])
	assert.Equal(t, 5.0, sample.gauges["source-record-poll-rate (c2)"])

	// sink-task-metrics: c3 has a single task.
	assert.Equal(t, 4.0, sample.gauges["sink-record-read-rate (c3)"])
	assert.Equal(t, 4.0, sample.gauges["sink-record-send-rate (c3)"])

	// The cluster-wide (non-per-connector) aggregates should still be summed as before.
	assert.Equal(t, 100.0, sample.gauges["incoming-byte-rate"])
	assert.Equal(t, 50.0, sample.gauges["outgoing-byte-rate"])
}
