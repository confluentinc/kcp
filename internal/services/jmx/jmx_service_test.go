package jmx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
		json.NewEncoder(w).Encode(response)
	}))
}

func TestComputeSnapshot_RatesFromCounterDeltas(t *testing.T) {
	prev := &rawSample{
		timestamp: time.Now(),
		counters:  map[string]float64{"BytesInPerSec": 10000, "BytesOutPerSec": 5000, "MessagesInPerSec": 100},
		gauges:    map[string]float64{"PartitionCount": 50, "ClientConnectionCount": 3, "TotalLocalStorageUsage": 1073741824},
	}
	curr := &rawSample{
		timestamp: prev.timestamp.Add(10 * time.Second),
		counters:  map[string]float64{"BytesInPerSec": 20000, "BytesOutPerSec": 10000, "MessagesInPerSec": 200},
		gauges:    map[string]float64{"PartitionCount": 50, "ClientConnectionCount": 5, "TotalLocalStorageUsage": 2147483648},
	}

	snapshot := computeSnapshot(prev, curr)

	assert.Equal(t, 1000.0, snapshot.metrics["BytesInPerSec"])
	assert.Equal(t, 500.0, snapshot.metrics["BytesOutPerSec"])
	assert.Equal(t, 10.0, snapshot.metrics["MessagesInPerSec"])
	assert.Equal(t, 50.0, snapshot.metrics["PartitionCount"])
	assert.Equal(t, 50.0, snapshot.metrics["GlobalPartitionCount"])
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

	snapshot := computeSnapshot(prev, curr)

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

	svc := NewJMXService([]string{server.URL})
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

func TestCollectOverDuration_DurationMustExceedInterval(t *testing.T) {
	svc := NewJMXService([]string{"http://localhost:1"})
	_, err := svc.CollectOverDuration(context.Background(), 5*time.Second, 5*time.Second)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be greater than")
}
