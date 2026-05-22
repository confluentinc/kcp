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
	"github.com/confluentinc/kcp/internal/types"
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
	svc := NewPrometheusService(promClient, BrokerQueryDefinitions(), nil)

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
	svc := NewPrometheusService(promClient, BrokerQueryDefinitions(), nil)

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

func TestPrometheusService_CollectMetrics_PopulatesQueryInfo(t *testing.T) {
	mockData := map[string][]float64{
		"bytesinpersec_total":    {1024.0},
		"bytesoutpersec_total":   {512.0},
		"messagesinpersec_total": {100.0},
		"partitioncount":         {50.0},
		"connection_count":       {10.0},
		"log_size":               {5.5},
	}

	server := newMockPrometheusServer(t, mockData)
	defer server.Close()

	promClient := client.NewPrometheusClient(server.URL)
	svc := NewPrometheusService(promClient, BrokerQueryDefinitions(), nil)

	result, err := svc.CollectMetrics(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.QueryInfo)

	// Should have one entry per query definition (7)
	assert.Len(t, result.QueryInfo, len(BrokerQueryDefinitions()))

	for _, qi := range result.QueryInfo {
		assert.Equal(t, types.MetricBackendPrometheus, qi.SourceType)
		assert.NotEmpty(t, qi.MetricName)
		assert.NotEmpty(t, qi.PromQLQuery)
		assert.NotEmpty(t, qi.PrometheusURL)
		assert.Contains(t, qi.PrometheusURL, server.URL)
		assert.NotEmpty(t, qi.PrometheusMetricName)
		assert.Contains(t, qi.CurlCommand, "curl")
		assert.Contains(t, qi.CurlCommand, server.URL)
		assert.NotEmpty(t, qi.AggregationNote)
		assert.NotEmpty(t, qi.Statistic)
		assert.Equal(t, int32(60), qi.Period)
		assert.Equal(t, "1d", qi.QueryDuration)
	}

	// Verify rate-based metrics have the rate window substituted
	for _, qi := range result.QueryInfo {
		assert.NotContains(t, qi.PromQLQuery, "%s", "rate window should be substituted")
	}

	// Verify specific metric names
	names := make(map[string]bool)
	for _, qi := range result.QueryInfo {
		names[qi.MetricName] = true
	}
	assert.True(t, names["BytesInPerSec"])
	assert.True(t, names["PartitionCount"])
	assert.True(t, names["TotalLocalStorageUsage"])
}

func TestBuildPrometheusQueryInfo(t *testing.T) {
	end := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	infos := buildPrometheusQueryInfo("http://prom:9090", "5m", 60*time.Second, 24*time.Hour, start, end, BrokerQueryDefinitions(), nil)

	assert.Len(t, infos, len(BrokerQueryDefinitions()))

	// Rate-based metrics should have rate() in the aggregation note
	for _, info := range infos[:3] {
		assert.Contains(t, info.PromQLQuery, "rate(")
		assert.Contains(t, info.PromQLQuery, "[5m]")
		assert.Contains(t, info.AggregationNote, "rate()")
	}

	// Gauge metrics should mention "gauge"
	partInfo := infos[3] // PartitionCount
	assert.Equal(t, "PartitionCount", partInfo.MetricName)
	assert.Contains(t, partInfo.AggregationNote, "gauge")

	// Storage metric should mention GiB conversion
	storageInfo := infos[6] // TotalLocalStorageUsage
	assert.Equal(t, "TotalLocalStorageUsage", storageInfo.MetricName)
	assert.Contains(t, storageInfo.AggregationNote, "GiB")

	// All should have Statistic, Period, and QueryDuration
	for _, info := range infos {
		assert.NotEmpty(t, info.Statistic)
		assert.Equal(t, int32(60), info.Period)
		assert.Equal(t, "1d", info.QueryDuration)
	}

	// All should have curl commands with actual timestamps
	for _, info := range infos {
		assert.Contains(t, info.CurlCommand, "http://prom:9090/api/v1/query_range")
		assert.Contains(t, info.CurlCommand, "start=2026-05-10T12:00:00Z")
		assert.Contains(t, info.CurlCommand, "end=2026-05-11T12:00:00Z")
		assert.Contains(t, info.CurlCommand, "step=60s")
	}
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

func TestApplyLabelFilter(t *testing.T) {
	labels := map[string]string{"job": "confluent/connect-jmx-exporter"}

	tests := []struct {
		name       string
		query      string
		metricName string
		labels     map[string]string
		expected   string
	}{
		{
			"no labels returns query unchanged",
			"sum(kafka_connect_worker_connector_count)",
			"kafka_connect_worker_connector_count",
			nil,
			"sum(kafka_connect_worker_connector_count)",
		},
		{
			"simple metric gets label selector",
			"kafka_connect_worker_task_count",
			"kafka_connect_worker_task_count",
			labels,
			`kafka_connect_worker_task_count{job="confluent/connect-jmx-exporter"}`,
		},
		{
			"sum-wrapped metric gets label selector",
			"sum(kafka_connect_worker_connector_count)",
			"kafka_connect_worker_connector_count",
			labels,
			`sum(kafka_connect_worker_connector_count{job="confluent/connect-jmx-exporter"})`,
		},
		{
			"rate-wrapped metric gets label selector",
			"sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[5m]))",
			"kafka_server_brokertopicmetrics_bytesinpersec_total",
			labels,
			`sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total{job="confluent/connect-jmx-exporter"}[5m]))`,
		},
		{
			"metric with existing selector gets labels appended",
			`kafka_controller_kafkacontroller_value{name="GlobalPartitionCount"}`,
			`kafka_controller_kafkacontroller_value{name="GlobalPartitionCount"}`,
			labels,
			`kafka_controller_kafkacontroller_value{job="confluent/connect-jmx-exporter",name="GlobalPartitionCount"}`,
		},
		{
			"empty metric name returns query unchanged",
			"sum(kafka_connect_worker_connector_count)",
			"",
			labels,
			"sum(kafka_connect_worker_connector_count)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyLabelFilter(tt.query, tt.metricName, tt.labels)
			assert.Equal(t, tt.expected, result)
		})
	}
}
