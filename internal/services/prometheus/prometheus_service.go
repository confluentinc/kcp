package prometheus

import (
	"context"
	"fmt"
	"log/slog"
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
}

// BrokerQueryDefinitions returns the standard Kafka broker Prometheus queries.
func BrokerQueryDefinitions() []MetricQuery {
	return []MetricQuery{
		{"BytesInPerSec", "sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[%s]))", "kafka_server_brokertopicmetrics_bytesinpersec_total"},
		{"BytesOutPerSec", "sum(rate(kafka_server_brokertopicmetrics_bytesoutpersec_total[%s]))", "kafka_server_brokertopicmetrics_bytesoutpersec_total"},
		{"MessagesInPerSec", "sum(rate(kafka_server_brokertopicmetrics_messagesinpersec_total[%s]))", "kafka_server_brokertopicmetrics_messagesinpersec_total"},
		{"PartitionCount", "sum(kafka_server_replicamanager_partitioncount)", "kafka_server_replicamanager_partitioncount"},
		{"GlobalPartitionCount", "kafka_controller_kafkacontroller_value{name=\"GlobalPartitionCount\"}", "kafka_controller_kafkacontroller_value{name=\"GlobalPartitionCount\"}"},
		{"ClientConnectionCount", "sum(kafka_server_socketservermetrics_connection_count)", "kafka_server_socketservermetrics_connection_count"},
		{"TotalLocalStorageUsage", "sum(kafka_log_log_size) / (1024*1024*1024)", "kafka_log_log_size"},
	}
}

// ConnectQueryDefinitions returns Prometheus queries for Kafka Connect worker metrics.
// Metric names match the JMX exporter naming convention (kafka_connect_worker_*).
// Client-level metrics (incoming/outgoing-byte-rate, connection-count, request-rate)
// require the JMX exporter to whitelist kafka.connect:client-id=*,type=connect-metrics.
func ConnectQueryDefinitions() []MetricQuery {
	return []MetricQuery{
		{"connector-count", "sum(kafka_connect_worker_connector_count)", "kafka_connect_worker_connector_count"},
		{"task-count", "sum(kafka_connect_worker_task_count)", "kafka_connect_worker_task_count"},
		{"source-record-write-rate", "sum(kafka_connect_source_task_source_record_write_rate)", "kafka_connect_source_task_source_record_write_rate"},
		{"source-record-poll-rate", "sum(kafka_connect_source_task_source_record_poll_rate)", "kafka_connect_source_task_source_record_poll_rate"},
		{"incoming-byte-rate", "sum(kafka_connect_network_io_incoming_byte_rate)", "kafka_connect_network_io_incoming_byte_rate"},
		{"outgoing-byte-rate", "sum(kafka_connect_network_io_outgoing_byte_rate)", "kafka_connect_network_io_outgoing_byte_rate"},
		{"connection-count", "sum(kafka_connect_network_io_connection_count)", "kafka_connect_network_io_connection_count"},
		{"request-rate", "sum(kafka_connect_network_io_request_rate)", "kafka_connect_network_io_request_rate"},
	}
}

// PrometheusService collects Kafka metrics from a Prometheus server
type PrometheusService struct {
	client  *client.PrometheusClient
	queries []MetricQuery
}

// NewPrometheusService creates a new Prometheus metrics service
func NewPrometheusService(promClient *client.PrometheusClient, queries []MetricQuery) *PrometheusService {
	return &PrometheusService{client: promClient, queries: queries}
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
			slog.Warn("Prometheus query returned no data points", "label", mq.Label, "query", query)
		}

		for _, result := range results {
			for _, dp := range result.Values {
				v := dp.Value
				dpStart := dp.Timestamp.Format(time.RFC3339)
				dpEnd := dp.Timestamp.Add(step).Format(time.RFC3339)
				allMetrics = append(allMetrics, types.ProcessedMetric{
					Start: dpStart,
					End:   dpEnd,
					Label: mq.Label,
					Value: &v,
				})
				valuesByLabel[mq.Label] = append(valuesByLabel[mq.Label], v)
			}
		}
	}

	if len(allMetrics) == 0 {
		slog.Warn("No metrics data was collected from Prometheus. Ensure your Prometheus instance is scraping the expected metrics.",
			"docs", build_info.DocsURL()+"osk-configuration/metrics-collection/#prometheus-promql-queries")
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
		QueryInfo:  buildPrometheusQueryInfo(s.client.BaseURL(), rateWindow, step, queryRange, start, end, s.queries),
	}, nil
}

// buildPrometheusQueryInfo generates MetricQueryInfo entries for all Prometheus metrics,
// including the resolved PromQL query and a curl command to reproduce it.
func buildPrometheusQueryInfo(promBaseURL, rateWindow string, step, queryRange time.Duration, start, end time.Time, queries []MetricQuery) []types.MetricQueryInfo {
	infos := make([]types.MetricQueryInfo, 0, len(queries))
	periodSec := int32(step.Seconds())
	durationStr := types.FormatQueryDuration(queryRange)

	for _, mq := range queries {
		resolvedQuery := mq.Query
		if strings.Contains(resolvedQuery, "%s") {
			resolvedQuery = fmt.Sprintf(resolvedQuery, rateWindow)
		}

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
