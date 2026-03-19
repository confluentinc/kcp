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

// mockJolokiaServer creates a test server that simulates Jolokia responses.
// Counter values increment on each call to simulate real monotonic counters.
func mockJolokiaServer(t *testing.T) *httptest.Server {
	t.Helper()

	var callCount atomic.Int64

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		var response map[string]any

		switch {
		case strings.Contains(r.URL.Path, "BytesInPerSec"):
			// Counter increments by 1000 per call
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"Count": float64(n * 1000),
				},
			}
		case strings.Contains(r.URL.Path, "BytesOutPerSec"):
			// Counter increments by 500 per call
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"Count": float64(n * 500),
				},
			}
		case strings.Contains(r.URL.Path, "MessagesInPerSec"):
			// Counter increments by 10 per call
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"Count": float64(n * 10),
				},
			}
		case strings.Contains(r.URL.Path, "PartitionCount"):
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"Value": 50.0,
				},
			}
		case strings.Contains(r.URL.Path, "socket-server-metrics"):
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"kafka.server:listener=PLAINTEXT,networkProcessor=0,type=socket-server-metrics": map[string]any{
						"connection-count": 2.0,
					},
					"kafka.server:listener=PLAINTEXT,networkProcessor=1,type=socket-server-metrics": map[string]any{
						"connection-count": 1.0,
					},
				},
			}
		case strings.Contains(r.URL.Path, "kafka.log"):
			response = map[string]any{
				"status": 200,
				"value": map[string]any{
					"kafka.log:name=Size,partition=0,topic=test,type=Log": map[string]any{
						"Value": 536870912.0, // 0.5 GB
					},
					"kafka.log:name=Size,partition=1,topic=test,type=Log": map[string]any{
						"Value": 536870912.0, // 0.5 GB
					},
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

func TestComputeSnapshot_RatesFromCounterDeltas(t *testing.T) {
	prev := &rawSample{
		timestamp: time.Now(),
		counters: map[string]float64{
			"BytesInPerSec":    10000,
			"BytesOutPerSec":   5000,
			"MessagesInPerSec": 100,
		},
		gauges: map[string]float64{
			"PartitionCount":       50,
			"ClientConnectionCount": 3,
			"TotalLocalStorageUsage": 1073741824, // 1 GB in bytes
		},
	}

	curr := &rawSample{
		timestamp: prev.timestamp.Add(10 * time.Second),
		counters: map[string]float64{
			"BytesInPerSec":    20000, // +10000 in 10s = 1000/s
			"BytesOutPerSec":   10000, // +5000 in 10s = 500/s
			"MessagesInPerSec": 200,   // +100 in 10s = 10/s
		},
		gauges: map[string]float64{
			"PartitionCount":       50,
			"ClientConnectionCount": 5,
			"TotalLocalStorageUsage": 2147483648, // 2 GB in bytes
		},
	}

	snapshot := computeSnapshot(prev, curr)

	// Rates computed from counter deltas
	assert.Equal(t, 1000.0, snapshot.Metrics["BytesInPerSec"])
	assert.Equal(t, 500.0, snapshot.Metrics["BytesOutPerSec"])
	assert.Equal(t, 10.0, snapshot.Metrics["MessagesInPerSec"])

	// Gauges taken from current sample
	assert.Equal(t, 50.0, snapshot.Metrics["PartitionCount"])
	assert.Equal(t, 50.0, snapshot.Metrics["GlobalPartitionCount"])
	assert.Equal(t, 5.0, snapshot.Metrics["ClientConnectionCount"])
	assert.Equal(t, 2.0, snapshot.Metrics["TotalLocalStorageUsage"]) // 2 GB

	assert.Len(t, snapshot.Metrics, 7)
}

func TestJMXService_CollectSnapshot(t *testing.T) {
	server := mockJolokiaServer(t)
	defer server.Close()

	svc := NewJMXService([]string{server.URL})
	snapshot, err := svc.CollectSnapshot(context.Background())

	require.NoError(t, err)
	require.NotNil(t, snapshot)

	// Rate metrics should be non-zero (computed from counter deltas)
	assert.Greater(t, snapshot.Metrics["BytesInPerSec"], 0.0)
	assert.Greater(t, snapshot.Metrics["BytesOutPerSec"], 0.0)
	assert.Greater(t, snapshot.Metrics["MessagesInPerSec"], 0.0)

	// Gauge metrics
	assert.Equal(t, 50.0, snapshot.Metrics["PartitionCount"])
	assert.Equal(t, 50.0, snapshot.Metrics["GlobalPartitionCount"])
	assert.Equal(t, 3.0, snapshot.Metrics["ClientConnectionCount"])

	assert.Len(t, snapshot.Metrics, 7)
}

func TestJMXService_CollectOverDuration(t *testing.T) {
	server := mockJolokiaServer(t)
	defer server.Close()

	svc := NewJMXService([]string{server.URL})

	ctx := context.Background()
	metrics, err := svc.CollectOverDuration(ctx, 3*time.Second, 1*time.Second)

	require.NoError(t, err)
	require.NotNil(t, metrics)

	assert.Equal(t, "3s", metrics.ScanDuration)
	assert.GreaterOrEqual(t, len(metrics.Snapshots), 2, "Expected at least 2 rate snapshots")

	// Each snapshot should have computed rates
	for i, snapshot := range metrics.Snapshots {
		assert.NotEmpty(t, snapshot.Metrics, "Snapshot %d should have metrics", i)
		assert.Greater(t, snapshot.Metrics["BytesInPerSec"], 0.0,
			"Snapshot %d BytesInPerSec should be positive", i)
	}

	// Snapshots should be in chronological order
	for i := 1; i < len(metrics.Snapshots); i++ {
		assert.True(t, metrics.Snapshots[i].Timestamp.After(metrics.Snapshots[i-1].Timestamp))
	}
}
