package prometheus

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

// MetricQuery defines a single Prometheus metric to collect.
type MetricQuery struct {
	Label string
	// Query is a format string. %s is replaced with the rate window (e.g. "5m", "4h").
	// Queries without %s are used as-is.
	Query string
	// PrometheusMetric is the raw Prometheus metric name used in the query.
	PrometheusMetric string
	// GroupByConnector indicates the query is aggregated with `sum by (connector) (...)`
	// and results should be broken out per connector using the `connector` series label.
	GroupByConnector bool
	// Overridden is true when PrometheusMetric came from a user-configured
	// metric-name override rather than the default series name. An overridden
	// query that still returns no data is actionable (the override was set
	// precisely to fix an empty result), so it is logged louder than a routine
	// empty default.
	Overridden bool
}

// BrokerQueryDefinitions returns the standard Kafka broker Prometheus queries.
// overrides maps a logical label (e.g. "BytesInPerSec") to the base series name
// this cluster's exporter actually exposes; an entry with an empty value is
// ignored. The overridden name is substituted into kcp's existing query wrapping
// and set as PrometheusMetric, so applyLabelFilter injection keeps working. Pass
// nil for the defaults.
func BrokerQueryDefinitions(overrides map[string]string) []MetricQuery {
	name := func(label, def string) (string, bool) {
		if v, ok := overrides[label]; ok && v != "" {
			return v, true
		}
		return def, false
	}

	bytesIn, bytesInOv := name("BytesInPerSec", "kafka_server_brokertopicmetrics_bytesinpersec_total")
	bytesOut, bytesOutOv := name("BytesOutPerSec", "kafka_server_brokertopicmetrics_bytesoutpersec_total")
	messagesIn, messagesInOv := name("MessagesInPerSec", "kafka_server_brokertopicmetrics_messagesinpersec_total")
	partitionCount, partitionCountOv := name("PartitionCount", "kafka_server_replicamanager_partitioncount")
	globalPartition, globalPartitionOv := name("GlobalPartitionCount", "kafka_controller_kafkacontroller_value")
	clientConn, clientConnOv := name("ClientConnectionCount", "kafka_server_socketservermetrics_connection_count")
	logSize, logSizeOv := name("TotalLocalStorageUsage", "kafka_log_log_size")

	// GlobalPartitionCount is distinguished by a {name="..."} discriminator on a
	// shared controller series; an override replaces the base series name. If the
	// override itself already carries a label selector (e.g. a job-scoped
	// series), the discriminator is merged into it rather than appended as a
	// second brace group, which would otherwise produce invalid PromQL.
	globalPartitionSel := appendNameDiscriminator(globalPartition, "GlobalPartitionCount")

	return []MetricQuery{
		{Label: "BytesInPerSec", Query: "sum(rate(" + bytesIn + "[%s]))", PrometheusMetric: bytesIn, Overridden: bytesInOv},
		{Label: "BytesOutPerSec", Query: "sum(rate(" + bytesOut + "[%s]))", PrometheusMetric: bytesOut, Overridden: bytesOutOv},
		{Label: "MessagesInPerSec", Query: "sum(rate(" + messagesIn + "[%s]))", PrometheusMetric: messagesIn, Overridden: messagesInOv},
		{Label: "PartitionCount", Query: "sum(" + partitionCount + ")", PrometheusMetric: partitionCount, Overridden: partitionCountOv},
		{Label: "GlobalPartitionCount", Query: globalPartitionSel, PrometheusMetric: globalPartitionSel, Overridden: globalPartitionOv},
		{Label: "ClientConnectionCount", Query: "sum(" + clientConn + ")", PrometheusMetric: clientConn, Overridden: clientConnOv},
		{Label: "TotalLocalStorageUsage", Query: "sum(" + logSize + ") / (1024*1024*1024)", PrometheusMetric: logSize, Overridden: logSizeOv},
	}
}

// ConnectQueryDefinitions returns Prometheus queries for Kafka Connect worker metrics.
// Metric names match the JMX exporter naming convention (kafka_connect_worker_*).
// Client-level metrics (incoming/outgoing-byte-rate, connection-count, request-rate)
// require the JMX exporter to whitelist kafka.connect:client-id=*,type=connect-metrics.
// Source/sink task metrics are grouped by connector (`sum by (connector) (...)`) so
// per-connector series can be broken out in CollectMetrics.
func ConnectQueryDefinitions() []MetricQuery {
	return []MetricQuery{
		{Label: "connector-count", Query: "sum(kafka_connect_worker_connector_count)", PrometheusMetric: "kafka_connect_worker_connector_count"},
		{Label: "task-count", Query: "sum(kafka_connect_worker_task_count)", PrometheusMetric: "kafka_connect_worker_task_count"},
		{Label: "incoming-byte-rate", Query: "sum(kafka_connect_metrics_incoming_byte_rate)", PrometheusMetric: "kafka_connect_metrics_incoming_byte_rate"},
		{Label: "outgoing-byte-rate", Query: "sum(kafka_connect_metrics_outgoing_byte_rate)", PrometheusMetric: "kafka_connect_metrics_outgoing_byte_rate"},
		{Label: "connection-count", Query: "sum(kafka_connect_metrics_connection_count)", PrometheusMetric: "kafka_connect_metrics_connection_count"},
		{Label: "request-rate", Query: "sum(kafka_connect_metrics_request_rate)", PrometheusMetric: "kafka_connect_metrics_request_rate"},
		{Label: "source-record-write-rate", Query: "sum by (connector) (kafka_connect_source_task_source_record_write_rate)", PrometheusMetric: "kafka_connect_source_task_source_record_write_rate", GroupByConnector: true},
		{Label: "source-record-poll-rate", Query: "sum by (connector) (kafka_connect_source_task_source_record_poll_rate)", PrometheusMetric: "kafka_connect_source_task_source_record_poll_rate", GroupByConnector: true},
		{Label: "sink-record-read-rate", Query: "sum by (connector) (kafka_connect_sink_task_sink_record_read_rate)", PrometheusMetric: "kafka_connect_sink_task_sink_record_read_rate", GroupByConnector: true},
		{Label: "sink-record-send-rate", Query: "sum by (connector) (kafka_connect_sink_task_sink_record_send_rate)", PrometheusMetric: "kafka_connect_sink_task_sink_record_send_rate", GroupByConnector: true},
	}
}

// PrometheusService collects Kafka metrics from a Prometheus server
type PrometheusService struct {
	client  *client.PrometheusClient
	queries []MetricQuery
	labels  map[string]string
}

// NewPrometheusService creates a new Prometheus metrics service.
// Labels is an optional map of Prometheus label selectors to scope queries
// to a specific target (e.g. {"job": "confluent/connect-jmx-exporter"}).
// Pass nil for no filtering.
func NewPrometheusService(promClient *client.PrometheusClient, queries []MetricQuery, labels map[string]string) *PrometheusService {
	return &PrometheusService{client: promClient, queries: queries, labels: labels}
}

// applyLabelFilter injects label selectors into a PromQL query by finding
// the metric name and appending {key="value",...} after it.
func applyLabelFilter(query, metricName string, labels map[string]string) string {
	if len(labels) == 0 || metricName == "" {
		return query
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		// Escape backslashes and double quotes in label values for valid PromQL
		v := strings.ReplaceAll(labels[k], `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, v))
	}
	labelStr := strings.Join(parts, ",")

	// Extract the base metric name (without any existing selector)
	baseName := metricName
	if idx := strings.Index(metricName, "{"); idx >= 0 {
		baseName = metricName[:idx]
	}

	// Find where the metric name appears in the query and look for existing braces
	idx := strings.Index(query, baseName)
	if idx < 0 {
		return query
	}

	afterName := idx + len(baseName)
	if afterName < len(query) && query[afterName] == '{' {
		// Insert our labels at the start of the existing selector
		return query[:afterName+1] + labelStr + "," + query[afterName+1:]
	}

	// No existing selector — add one
	return query[:afterName] + "{" + labelStr + "}" + query[afterName:]
}

// appendNameDiscriminator appends a {name="value"} label matcher to a
// Prometheus series name, merging it into an existing selector if the series
// already carries one (e.g. from a metric-name override that points at a
// job-scoped series) rather than producing a second, invalid brace group.
func appendNameDiscriminator(series, name string) string {
	if idx := strings.Index(series, "{"); idx >= 0 {
		return series[:idx+1] + fmt.Sprintf(`name="%s",`, name) + series[idx+1:]
	}
	return series + fmt.Sprintf(`{name="%s"}`, name)
}

// SelectStep chooses an appropriate query step based on the time range
func SelectStep(queryRange time.Duration) time.Duration {
	switch {
	case queryRange <= 24*time.Hour:
		return 1 * time.Minute
	case queryRange <= 7*24*time.Hour:
		return 5 * time.Minute
	case queryRange <= 30*24*time.Hour:
		return 1 * time.Hour
	default:
		return 2 * time.Hour
	}
}

// selectRateWindow returns a Prometheus range window string (e.g. "5m", "4h")
// that is at least 4x the step to ensure rate() has enough data points.
func selectRateWindow(step time.Duration) string {
	window := step * 4
	if window < 5*time.Minute {
		window = 5 * time.Minute
	}
	minutes := int(window.Minutes())
	if minutes >= 60 && minutes%60 == 0 {
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dm", minutes)
}

// CollectMetrics queries Prometheus for all Kafka metrics over the specified range
func (s *PrometheusService) CollectMetrics(ctx context.Context, queryRange time.Duration) (*types.ProcessedClusterMetrics, error) {
	end := time.Now()
	start := end.Add(-queryRange)
	step := SelectStep(queryRange)
	rateWindow := selectRateWindow(step)

	var allMetrics []types.ProcessedMetric
	valuesByLabel := make(map[string][]float64)

	for _, mq := range s.queries {
		query := mq.Query
		if strings.Contains(query, "%s") {
			query = fmt.Sprintf(query, rateWindow)
		}
		query = applyLabelFilter(query, mq.PrometheusMetric, s.labels)
		results, err := s.client.QueryRange(ctx, query, start, end, step)
		if err != nil {
			slog.Warn("Prometheus query failed, skipping metric", "label", mq.Label, "error", err)
			continue
		}

		dataPoints := 0
		for _, r := range results {
			dataPoints += len(r.Values)
		}
		if dataPoints == 0 {
			if mq.Overridden {
				slog.Warn("⚠️ Overridden Prometheus metric returned no data points — check the configured metric_names value", "label", mq.Label, "query", query)
			} else {
				slog.Debug("Prometheus query returned no data points", "label", mq.Label, "query", query)
			}
		}

		for _, result := range results {
			label := mq.Label
			if mq.GroupByConnector {
				connector := result.Labels["connector"]
				if connector == "" {
					slog.Warn("⚠️ per-connector query result missing 'connector' label, skipping series", "label", mq.Label)
					continue
				}
				label = fmt.Sprintf("%s (%s)", mq.Label, connector)
			}

			for _, dp := range result.Values {
				v := dp.Value
				dpStart := dp.Timestamp.Format(time.RFC3339)
				dpEnd := dp.Timestamp.Add(step).Format(time.RFC3339)
				allMetrics = append(allMetrics, types.ProcessedMetric{
					Start: dpStart,
					End:   dpEnd,
					Label: label,
					Value: &v,
				})
				valuesByLabel[label] = append(valuesByLabel[label], v)
			}
		}
	}

	if len(allMetrics) == 0 {
		slog.Warn("No metrics data was collected from Prometheus. Ensure your Prometheus instance is scraping the expected metrics.",
			"docs", build_info.DocsURL()+"apache-kafka-configuration/metrics-collection/#prometheus-promql-queries")
	}

	aggregates := calculateAggregates(valuesByLabel)

	return &types.ProcessedClusterMetrics{
		Metadata: types.MetricMetadata{
			StartDate: start,
			EndDate:   end,
			Period:    int32(step.Seconds()),
		},
		Metrics:    allMetrics,
		Aggregates: aggregates,
		QueryInfo:  buildPrometheusQueryInfo(s.client.BaseURL(), rateWindow, step, queryRange, start, end, s.queries, s.labels),
	}, nil
}

// buildPrometheusQueryInfo generates MetricQueryInfo entries for all Prometheus metrics,
// including the resolved PromQL query and a curl command to reproduce it.
func buildPrometheusQueryInfo(promBaseURL, rateWindow string, step, queryRange time.Duration, start, end time.Time, queries []MetricQuery, labels map[string]string) []types.MetricQueryInfo {
	infos := make([]types.MetricQueryInfo, 0, len(queries))
	periodSec := int32(step.Seconds())
	durationStr := types.FormatQueryDuration(queryRange)

	for _, mq := range queries {
		resolvedQuery := mq.Query
		if strings.Contains(resolvedQuery, "%s") {
			resolvedQuery = fmt.Sprintf(resolvedQuery, rateWindow)
		}
		resolvedQuery = applyLabelFilter(resolvedQuery, mq.PrometheusMetric, labels)

		var statistic string
		var note string
		switch {
		case strings.Contains(mq.Query, "rate("):
			statistic = fmt.Sprintf("Rate (sum of rate() over %s window)", rateWindow)
			note = fmt.Sprintf(
				"Computes rate() over a %s window, then sums across all instances. Query step: %ds.",
				rateWindow, int(step.Seconds()))
		case strings.Contains(resolvedQuery, "/ (1024*1024*1024)"):
			statistic = "Sum (bytes converted to GiB)"
			note = fmt.Sprintf(
				"Sums raw byte values across all instances and converts to GiB. Query step: %ds.",
				int(step.Seconds()))
		default:
			statistic = "Sum across instances"
			note = fmt.Sprintf(
				"Sums the gauge value across all instances. Query step: %ds.",
				int(step.Seconds()))
		}

		infos = append(infos, types.MetricQueryInfo{
			MetricName:           mq.Label,
			SourceType:           types.MetricBackendPrometheus,
			Statistic:            statistic,
			Period:               periodSec,
			QueryDuration:        durationStr,
			PromQLQuery:          resolvedQuery,
			PrometheusURL:        promBaseURL,
			PrometheusMetricName: mq.PrometheusMetric,
			CurlCommand:          fmt.Sprintf("curl -G '%s/api/v1/query_range' --data-urlencode 'query=%s' --data-urlencode 'start=%s' --data-urlencode 'end=%s' --data-urlencode 'step=%ds'", promBaseURL, resolvedQuery, start.Format(time.RFC3339), end.Format(time.RFC3339), int(step.Seconds())),
			AggregationNote:      note,
			LabelFilter:          labels,
		})
	}

	return infos
}

func calculateAggregates(valuesByLabel map[string][]float64) map[string]types.MetricAggregate {
	aggregates := make(map[string]types.MetricAggregate)
	for label, values := range valuesByLabel {
		if len(values) == 0 {
			continue
		}
		min, max, sum := values[0], values[0], 0.0
		for _, v := range values {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
			sum += v
		}
		avg := sum / float64(len(values))
		aggregates[label] = types.MetricAggregate{
			Average: &avg,
			Maximum: &max,
			Minimum: &min,
		}
	}
	return aggregates
}
