package jmx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockJolokiaServer creates a test HTTP server that simulates Jolokia responses
func mockJolokiaServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Jolokia client constructs URLs as: {baseURL}/jolokia/read/{mbeanPath}
		// We need to handle paths like: /jolokia/read/kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec

		var response map[string]any

		// Route by MBean name in the URL path
		switch {
		case strings.Contains(r.URL.Path, "BytesInPerSec"):
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"OneMinuteRate":     1000.5,
					"FiveMinuteRate":    950.2,
					"FifteenMinuteRate": 900.8,
					"Count":             50000.0,
					"MeanRate":          800.3,
				},
			}
		case strings.Contains(r.URL.Path, "BytesOutPerSec"):
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"OneMinuteRate":     2000.5,
					"FiveMinuteRate":    1950.2,
					"FifteenMinuteRate": 1900.8,
					"Count":             100000.0,
					"MeanRate":          1800.3,
				},
			}
		case strings.Contains(r.URL.Path, "MessagesInPerSec"):
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"OneMinuteRate":     100.5,
					"FiveMinuteRate":    95.2,
					"FifteenMinuteRate": 90.8,
					"Count":             5000.0,
					"MeanRate":          80.3,
				},
			}
		case strings.Contains(r.URL.Path, "PartitionCount"):
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"Value": 50.0,
				},
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			response = map[string]any{
				"status": 404,
				"error":  "MBean not found",
			}
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(response)
		require.NoError(t, err)
	}))
}

func TestJMXService_CollectSnapshot(t *testing.T) {
	// Create mock Jolokia server
	server := mockJolokiaServer(t)
	defer server.Close()

	// Create JMX service with two broker endpoints (to test summing)
	endpoints := []string{server.URL, server.URL}
	svc := NewJMXService(endpoints)

	// Collect snapshot
	ctx := context.Background()
	snapshot, err := svc.CollectSnapshot(ctx)
	require.NoError(t, err)
	require.NotNil(t, snapshot)

	// Verify timestamp
	assert.WithinDuration(t, time.Now(), snapshot.Timestamp, 2*time.Second)

	// Verify metrics - values should be summed across both brokers (doubled)
	// BytesInPerSec rate metrics
	assert.Equal(t, 2001.0, snapshot.Metrics["BytesInPerSec_OneMinuteRate"])     // 1000.5 * 2
	assert.Equal(t, 1900.4, snapshot.Metrics["BytesInPerSec_FiveMinuteRate"])    // 950.2 * 2
	assert.Equal(t, 1801.6, snapshot.Metrics["BytesInPerSec_FifteenMinuteRate"]) // 900.8 * 2
	assert.Equal(t, 100000.0, snapshot.Metrics["BytesInPerSec_Count"])           // 50000 * 2
	assert.Equal(t, 1600.6, snapshot.Metrics["BytesInPerSec_MeanRate"])          // 800.3 * 2

	// BytesOutPerSec rate metrics
	assert.Equal(t, 4001.0, snapshot.Metrics["BytesOutPerSec_OneMinuteRate"])     // 2000.5 * 2
	assert.Equal(t, 3900.4, snapshot.Metrics["BytesOutPerSec_FiveMinuteRate"])    // 1950.2 * 2
	assert.Equal(t, 3801.6, snapshot.Metrics["BytesOutPerSec_FifteenMinuteRate"]) // 1900.8 * 2
	assert.Equal(t, 200000.0, snapshot.Metrics["BytesOutPerSec_Count"])           // 100000 * 2
	assert.Equal(t, 3600.6, snapshot.Metrics["BytesOutPerSec_MeanRate"])          // 1800.3 * 2

	// MessagesInPerSec rate metrics
	assert.Equal(t, 201.0, snapshot.Metrics["MessagesInPerSec_OneMinuteRate"])     // 100.5 * 2
	assert.Equal(t, 190.4, snapshot.Metrics["MessagesInPerSec_FiveMinuteRate"])    // 95.2 * 2
	assert.Equal(t, 181.6, snapshot.Metrics["MessagesInPerSec_FifteenMinuteRate"]) // 90.8 * 2
	assert.Equal(t, 10000.0, snapshot.Metrics["MessagesInPerSec_Count"])           // 5000 * 2
	assert.Equal(t, 160.6, snapshot.Metrics["MessagesInPerSec_MeanRate"])          // 80.3 * 2

	// PartitionCount non-rate metric
	assert.Equal(t, 100.0, snapshot.Metrics["PartitionCount"]) // 50 * 2

	// Verify total number of metrics
	// 4 MBeans: 3 rate MBeans (5 fields each) + 1 non-rate MBean (1 field) = 16 metrics
	assert.Len(t, snapshot.Metrics, 16)
}

func TestJMXService_CollectOverDuration(t *testing.T) {
	// Create mock Jolokia server
	server := mockJolokiaServer(t)
	defer server.Close()

	// Create JMX service
	endpoints := []string{server.URL}
	svc := NewJMXService(endpoints)

	// Collect over 3 seconds with 1 second interval
	// Should get: 1 initial snapshot + snapshots at 1s, 2s = at least 3 snapshots
	ctx := context.Background()
	duration := 3 * time.Second
	interval := 1 * time.Second

	metrics, err := svc.CollectOverDuration(ctx, duration, interval)
	require.NoError(t, err)
	require.NotNil(t, metrics)

	// Verify metadata
	assert.Equal(t, duration.String(), metrics.ScanDuration)
	assert.WithinDuration(t, time.Now(), metrics.ScanStartTime, 5*time.Second)

	// Verify we got at least 3 snapshots
	assert.GreaterOrEqual(t, len(metrics.Snapshots), 3, "Expected at least 3 snapshots")

	// Verify each snapshot has metrics
	for i, snapshot := range metrics.Snapshots {
		assert.NotEmpty(t, snapshot.Metrics, "Snapshot %d should have metrics", i)
		assert.NotZero(t, snapshot.Timestamp, "Snapshot %d should have timestamp", i)
	}

	// Verify snapshots are in chronological order
	for i := 1; i < len(metrics.Snapshots); i++ {
		assert.True(t, metrics.Snapshots[i].Timestamp.After(metrics.Snapshots[i-1].Timestamp),
			"Snapshots should be in chronological order")
	}
}
