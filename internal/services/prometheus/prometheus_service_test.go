package prometheus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
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
	svc := NewPrometheusService(promClient, BrokerQueryDefinitions(nil), nil)

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
	svc := NewPrometheusService(promClient, BrokerQueryDefinitions(nil), nil)

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
	svc := NewPrometheusService(promClient, BrokerQueryDefinitions(nil), nil)

	result, err := svc.CollectMetrics(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.QueryInfo)

	// Should have one entry per query definition (7)
	assert.Len(t, result.QueryInfo, len(BrokerQueryDefinitions(nil)))

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

func TestBrokerQueryDefinitions_NoOverrideRegression(t *testing.T) {
	// With no overrides, output must be byte-identical to the historical defaults.
	expected := []MetricQuery{
		{Label: "BytesInPerSec", Query: "sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[%s]))", PrometheusMetric: "kafka_server_brokertopicmetrics_bytesinpersec_total"},
		{Label: "BytesOutPerSec", Query: "sum(rate(kafka_server_brokertopicmetrics_bytesoutpersec_total[%s]))", PrometheusMetric: "kafka_server_brokertopicmetrics_bytesoutpersec_total"},
		{Label: "MessagesInPerSec", Query: "sum(rate(kafka_server_brokertopicmetrics_messagesinpersec_total[%s]))", PrometheusMetric: "kafka_server_brokertopicmetrics_messagesinpersec_total"},
		{Label: "PartitionCount", Query: "sum(kafka_server_replicamanager_partitioncount)", PrometheusMetric: "kafka_server_replicamanager_partitioncount"},
		{Label: "GlobalPartitionCount", Query: "kafka_controller_kafkacontroller_value{name=\"GlobalPartitionCount\"}", PrometheusMetric: "kafka_controller_kafkacontroller_value{name=\"GlobalPartitionCount\"}"},
		{Label: "ClientConnectionCount", Query: "sum(kafka_server_socketservermetrics_connection_count)", PrometheusMetric: "kafka_server_socketservermetrics_connection_count"},
		{Label: "TotalLocalStorageUsage", Query: "sum(kafka_log_log_size) / (1024*1024*1024)", PrometheusMetric: "kafka_log_log_size"},
	}

	assert.Equal(t, expected, BrokerQueryDefinitions(nil))
	assert.Equal(t, expected, BrokerQueryDefinitions(map[string]string{}))
}

func TestBrokerQueryDefinitions_Override(t *testing.T) {
	overrides := map[string]string{
		"BytesInPerSec":          "acme_broker_bytesin_total",
		"PartitionCount":         "acme_partition_count",
		"TotalLocalStorageUsage": "acme_log_size",
	}
	defs := BrokerQueryDefinitions(overrides)

	byLabel := make(map[string]MetricQuery)
	for _, d := range defs {
		byLabel[d.Label] = d
	}

	// Rate-wrapped metric: series substituted, %s rate window preserved, flagged overridden.
	bytesIn := byLabel["BytesInPerSec"]
	assert.Equal(t, "sum(rate(acme_broker_bytesin_total[%s]))", bytesIn.Query)
	assert.Equal(t, "acme_broker_bytesin_total", bytesIn.PrometheusMetric)
	assert.True(t, bytesIn.Overridden)

	// Sum-wrapped gauge.
	partition := byLabel["PartitionCount"]
	assert.Equal(t, "sum(acme_partition_count)", partition.Query)
	assert.Equal(t, "acme_partition_count", partition.PrometheusMetric)
	assert.True(t, partition.Overridden)

	// Arithmetic-wrapped metric keeps its conversion.
	storage := byLabel["TotalLocalStorageUsage"]
	assert.Equal(t, "sum(acme_log_size) / (1024*1024*1024)", storage.Query)
	assert.Equal(t, "acme_log_size", storage.PrometheusMetric)
	assert.True(t, storage.Overridden)

	// A metric with no override keeps its default and is not flagged.
	bytesOut := byLabel["BytesOutPerSec"]
	assert.Equal(t, "sum(rate(kafka_server_brokertopicmetrics_bytesoutpersec_total[%s]))", bytesOut.Query)
	assert.False(t, bytesOut.Overridden)

	// An empty override value is ignored (treated as no override).
	defsEmpty := BrokerQueryDefinitions(map[string]string{"BytesInPerSec": ""})
	for _, d := range defsEmpty {
		if d.Label == "BytesInPerSec" {
			assert.Equal(t, "kafka_server_brokertopicmetrics_bytesinpersec_total", d.PrometheusMetric)
			assert.False(t, d.Overridden)
		}
	}
}

func TestBrokerQueryDefinitions_OverridePreservesLabelFilterInjection(t *testing.T) {
	defs := BrokerQueryDefinitions(map[string]string{"BytesInPerSec": "acme_broker_bytesin_total"})
	var bytesIn MetricQuery
	for _, d := range defs {
		if d.Label == "BytesInPerSec" {
			bytesIn = d
		}
	}

	// Resolve the rate window as CollectMetrics would, then inject label selectors.
	resolved := fmt.Sprintf(bytesIn.Query, "5m")
	filtered := applyLabelFilter(resolved, bytesIn.PrometheusMetric, map[string]string{"job": "acme"})
	assert.Equal(t, "sum(rate(acme_broker_bytesin_total{job=\"acme\"}[5m]))", filtered)
}

func TestBrokerQueryDefinitions_LabelsMatchCanonicalSet(t *testing.T) {
	var labels []string
	for _, d := range BrokerQueryDefinitions(nil) {
		labels = append(labels, d.Label)
	}
	assert.ElementsMatch(t, types.ValidBrokerMetricLabels(), labels,
		"BrokerQueryDefinitions labels must match the canonical override label set")
}

// TestBrokerQueryDefinitions_GlobalPartitionCountOverride_BareSeriesName
// covers the common case: the override is a bare series name with no
// selector of its own, so the {name="..."} discriminator is simply appended.
func TestBrokerQueryDefinitions_GlobalPartitionCountOverride_BareSeriesName(t *testing.T) {
	defs := BrokerQueryDefinitions(map[string]string{"GlobalPartitionCount": "acme_controller_value"})

	var global MetricQuery
	for _, d := range defs {
		if d.Label == "GlobalPartitionCount" {
			global = d
		}
	}

	assert.Equal(t, `acme_controller_value{name="GlobalPartitionCount"}`, global.Query)
	assert.Equal(t, global.Query, global.PrometheusMetric)
	assert.True(t, global.Overridden)
}

// TestBrokerQueryDefinitions_GlobalPartitionCountOverride_ExistingSelectorMerges
// is a regression test: GlobalPartitionCount's query is built by appending a
// static {name="GlobalPartitionCount"} discriminator to the override value. If
// the override itself already carries a label selector (e.g. it points at a
// job-scoped series), naively appending a second brace group produces invalid
// PromQL (two adjacent selectors on one series). The discriminator must be
// merged into the existing selector instead.
func TestBrokerQueryDefinitions_GlobalPartitionCountOverride_ExistingSelectorMerges(t *testing.T) {
	defs := BrokerQueryDefinitions(map[string]string{
		"GlobalPartitionCount": `acme_controller_value{job="acme"}`,
	})

	var global MetricQuery
	for _, d := range defs {
		if d.Label == "GlobalPartitionCount" {
			global = d
		}
	}

	// A single, valid selector — not two adjacent brace groups.
	assert.Equal(t, `acme_controller_value{name="GlobalPartitionCount",job="acme"}`, global.Query)
	assert.Equal(t, global.Query, global.PrometheusMetric)
	assert.NotContains(t, global.Query, "}{", "must not produce two adjacent brace groups")
	assert.True(t, global.Overridden)
}

func TestAppendNameDiscriminator(t *testing.T) {
	tests := []struct {
		name   string
		series string
		want   string
	}{
		{"no existing selector", "acme_controller_value", `acme_controller_value{name="GlobalPartitionCount"}`},
		{"merges into existing selector", `acme_controller_value{job="acme"}`, `acme_controller_value{name="GlobalPartitionCount",job="acme"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, appendNameDiscriminator(tt.series, "GlobalPartitionCount"))
		})
	}
}

func TestPrometheusService_CollectMetrics_OverriddenEmptyIsLoud(t *testing.T) {
	// Only PartitionCount returns data; every other query is empty. This keeps
	// allMetrics non-empty so the generic "no metrics collected" warning stays
	// silent and we isolate the per-metric empty signal.
	mockData := map[string][]float64{
		"partitioncount": {50.0, 50.0},
	}
	server := newMockPrometheusServer(t, mockData)
	defer server.Close()

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	promClient := client.NewPrometheusClient(server.URL)
	// Override BytesInPerSec to a series the mock has no data for.
	defs := BrokerQueryDefinitions(map[string]string{"BytesInPerSec": "totally_missing_series"})
	svc := NewPrometheusService(promClient, defs, nil)

	_, err := svc.CollectMetrics(context.Background(), 24*time.Hour)
	require.NoError(t, err)

	// An overridden query that still returns nothing is actionable → WARN.
	// A non-overridden empty query (e.g. BytesOutPerSec) stays quiet (DEBUG).
	var warnedBytesIn, warnedBytesOut bool
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if !strings.Contains(line, "level=WARN") {
			continue
		}
		if strings.Contains(line, "BytesInPerSec") {
			warnedBytesIn = true
		}
		if strings.Contains(line, "BytesOutPerSec") {
			warnedBytesOut = true
		}
	}
	assert.True(t, warnedBytesIn, "overridden-but-empty metric should be logged at WARN")
	assert.False(t, warnedBytesOut, "non-overridden empty metric should not be logged at WARN")
}

func TestBuildPrometheusQueryInfo(t *testing.T) {
	end := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	infos := buildPrometheusQueryInfo("http://prom:9090", "5m", 60*time.Second, 24*time.Hour, start, end, BrokerQueryDefinitions(nil), nil)

	assert.Len(t, infos, len(BrokerQueryDefinitions(nil)))

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
		{
			"multiple labels are sorted deterministically",
			"sum(kafka_connect_worker_connector_count)",
			"kafka_connect_worker_connector_count",
			map[string]string{"namespace": "confluent", "job": "connect-exporter"},
			`sum(kafka_connect_worker_connector_count{job="connect-exporter",namespace="confluent"})`,
		},
		{
			"label values with special characters are escaped",
			"sum(kafka_connect_worker_connector_count)",
			"kafka_connect_worker_connector_count",
			map[string]string{"job": `value"with\quotes`},
			`sum(kafka_connect_worker_connector_count{job="value\"with\\quotes"})`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyLabelFilter(tt.query, tt.metricName, tt.labels)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// newMockPrometheusServerPerConnector returns an httptest server that responds to
// range queries containing metricName with two matrix series, each carrying a
// distinct "connector" label, so per-connector grouping behavior can be exercised.
func newMockPrometheusServerPerConnector(t *testing.T, metricName string, connectorValues map[string][]float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")

		result := []map[string]interface{}{}
		if strings.Contains(query, metricName) {
			// Sort connector names for deterministic output across test runs.
			connectors := make([]string, 0, len(connectorValues))
			for c := range connectorValues {
				connectors = append(connectors, c)
			}
			sort.Strings(connectors)

			baseTime := float64(1710000000)
			for _, connector := range connectors {
				var matrixValues [][]interface{}
				for i, v := range connectorValues[connector] {
					matrixValues = append(matrixValues, []interface{}{
						baseTime + float64(i)*3600,
						fmt.Sprintf("%f", v),
					})
				}
				metric := map[string]string{"__name__": metricName}
				if connector != "" {
					metric["connector"] = connector
				}
				result = append(result, map[string]interface{}{
					"metric": metric,
					"values": matrixValues,
				})
			}
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

func TestPrometheusService_CollectMetrics_GroupByConnector(t *testing.T) {
	server := newMockPrometheusServerPerConnector(t, "kafka_connect_source_task_source_record_write_rate", map[string][]float64{
		"c1": {1.0, 2.0},
		"c2": {3.0, 4.0},
	})
	defer server.Close()

	promClient := client.NewPrometheusClient(server.URL)
	queries := []MetricQuery{
		{
			Label:            "source-record-write-rate",
			Query:            "sum by (connector) (kafka_connect_source_task_source_record_write_rate)",
			PrometheusMetric: "kafka_connect_source_task_source_record_write_rate",
			GroupByConnector: true,
		},
	}
	svc := NewPrometheusService(promClient, queries, nil)

	result, err := svc.CollectMetrics(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, result)

	labels := map[string]int{}
	for _, m := range result.Metrics {
		labels[m.Label]++
	}

	assert.Equal(t, 2, labels["source-record-write-rate (c1)"])
	assert.Equal(t, 2, labels["source-record-write-rate (c2)"])
	assert.NotContains(t, labels, "source-record-write-rate")

	c1Agg, ok := result.Aggregates["source-record-write-rate (c1)"]
	require.True(t, ok)
	assert.InDelta(t, 1.0, *c1Agg.Minimum, 0.001)
	assert.InDelta(t, 2.0, *c1Agg.Maximum, 0.001)

	c2Agg, ok := result.Aggregates["source-record-write-rate (c2)"]
	require.True(t, ok)
	assert.InDelta(t, 3.0, *c2Agg.Minimum, 0.001)
	assert.InDelta(t, 4.0, *c2Agg.Maximum, 0.001)

	// query_info stays one entry per MetricQuery, keyed by the base label.
	require.Len(t, result.QueryInfo, 1)
	assert.Equal(t, "source-record-write-rate", result.QueryInfo[0].MetricName)
}

func TestPrometheusService_CollectMetrics_GroupByConnector_MissingConnectorLabelSkipsSeries(t *testing.T) {
	// A series with no "connector" label at all (e.g. a misbehaving exporter or a
	// query that isn't actually grouped) must be skipped, not merged into a bare label.
	server := newMockPrometheusServerPerConnector(t, "kafka_connect_source_task_source_record_write_rate", map[string][]float64{
		"":   {99.0},
		"c1": {1.0, 2.0},
	})
	defer server.Close()

	promClient := client.NewPrometheusClient(server.URL)
	queries := []MetricQuery{
		{
			Label:            "source-record-write-rate",
			Query:            "sum by (connector) (kafka_connect_source_task_source_record_write_rate)",
			PrometheusMetric: "kafka_connect_source_task_source_record_write_rate",
			GroupByConnector: true,
		},
	}
	svc := NewPrometheusService(promClient, queries, nil)

	result, err := svc.CollectMetrics(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, result)

	labels := map[string]int{}
	for _, m := range result.Metrics {
		labels[m.Label]++
	}

	assert.Equal(t, 2, labels["source-record-write-rate (c1)"])
	assert.NotContains(t, labels, "source-record-write-rate")
	// Total metrics should only reflect the c1 series (2 points); the unlabeled
	// series (1 point) must have been skipped entirely.
	assert.Len(t, result.Metrics, 2)
}

func TestConnectQueryDefinitions_GroupByConnector(t *testing.T) {
	defs := ConnectQueryDefinitions()

	grouped := map[string]MetricQuery{}
	for _, mq := range defs {
		if mq.GroupByConnector {
			grouped[mq.Label] = mq
		}
	}

	expectedGrouped := []string{
		"source-record-write-rate",
		"source-record-poll-rate",
		"sink-record-read-rate",
		"sink-record-send-rate",
	}
	for _, label := range expectedGrouped {
		mq, ok := grouped[label]
		require.True(t, ok, "expected %s to be a GroupByConnector query", label)
		assert.Contains(t, mq.Query, "sum by (connector)")
	}
	assert.Len(t, grouped, len(expectedGrouped))

	// Worker/client-level queries remain plain sum(...) without grouping.
	for _, mq := range defs {
		if mq.GroupByConnector {
			continue
		}
		assert.Contains(t, mq.Query, "sum(")
		assert.NotContains(t, mq.Query, "by (connector)")
	}
}
