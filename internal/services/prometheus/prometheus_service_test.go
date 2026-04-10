package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockPrometheusServer(t *testing.T, metrics map[string][]float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")

		// Find matching metric data by checking if the query contains the metric key
		var values []float64
		for metricKey, vals := range metrics {
			if strings.Contains(query, metricKey) {
				values = vals
				break
			}
		}

		// Build matrix result
		var matrixValues [][]interface{}
		baseTime := float64(1710000000)
		for i, v := range values {
			matrixValues = append(matrixValues, []interface{}{
				baseTime + float64(i)*3600,
				fmt.Sprintf("%f", v),
			})
		}

		result := []map[string]interface{}{}
		if len(matrixValues) > 0 {
			result = append(result, map[string]interface{}{
				"metric": map[string]string{},
				"values": matrixValues,
			})
		}

		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result":     result,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestPrometheusService_CollectMetrics(t *testing.T) {
	// Map metric name substrings to mock data (matched via strings.Contains)
	mockData := map[string][]float64{
		"bytesinpersec_total":    {1024.0, 2048.0, 1500.0},
		"bytesoutpersec_total":   {512.0, 1024.0, 768.0},
		"messagesinpersec_total": {100.0, 200.0, 150.0},
		"partitioncount":         {50.0, 50.0, 50.0},
		"connection_count":       {10.0, 15.0, 12.0},
		"log_size":               {5.5, 5.6, 5.7},
	}

	server := newMockPrometheusServer(t, mockData)
	defer server.Close()

	promClient := client.NewPrometheusClient(server.URL)
	svc := NewPrometheusService(promClient)

	result, err := svc.CollectMetrics(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have metrics for all 7 labels × 3 data points each = 21 metrics
	assert.NotEmpty(t, result.Metrics)
	assert.NotEmpty(t, result.Aggregates)

	// Check aggregates exist for key metrics
	for _, label := range []string{"BytesInPerSec", "BytesOutPerSec", "MessagesInPerSec", "PartitionCount"} {
		agg, ok := result.Aggregates[label]
		assert.True(t, ok, "missing aggregate for %s", label)
		assert.NotNil(t, agg.Average)
		assert.NotNil(t, agg.Maximum)
		assert.NotNil(t, agg.Minimum)
	}

	// Verify BytesInPerSec aggregates
	bytesIn := result.Aggregates["BytesInPerSec"]
	assert.InDelta(t, 1024.0, *bytesIn.Minimum, 0.1)
	assert.InDelta(t, 2048.0, *bytesIn.Maximum, 0.1)
	assert.InDelta(t, 1524.0, *bytesIn.Average, 0.1)

	// Check metadata
	assert.Equal(t, int32(60), result.Metadata.Period) // 1-day range → 1m step
}

func TestPrometheusService_CollectMetrics_MissingMetric(t *testing.T) {
	// Only provide BytesInPerSec, all other queries return empty
	mockData := map[string][]float64{
		"bytesinpersec_total": {1024.0, 2048.0},
	}

	server := newMockPrometheusServer(t, mockData)
	defer server.Close()

	promClient := client.NewPrometheusClient(server.URL)
	svc := NewPrometheusService(promClient)

	result, err := svc.CollectMetrics(context.Background(), 7*24*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have data for BytesInPerSec only
	assert.NotEmpty(t, result.Metrics)
	_, hasBytesIn := result.Aggregates["BytesInPerSec"]
	assert.True(t, hasBytesIn)

	// 7-day range → 5m step
	assert.Equal(t, int32(300), result.Metadata.Period)
}

func TestPrometheusService_StepSelection(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected time.Duration
	}{
		{"1 day", 24 * time.Hour, 1 * time.Minute},
		{"7 days", 7 * 24 * time.Hour, 5 * time.Minute},
		{"30 days", 30 * 24 * time.Hour, 1 * time.Hour},
		{"90 days", 90 * 24 * time.Hour, 2 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := SelectStep(tt.duration)
			assert.Equal(t, tt.expected, step)
		})
	}
}
