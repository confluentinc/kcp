package prometheus

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

type metricQuery struct {
	Label string
	// Query is a format string. %s is replaced with the rate window (e.g. "5m", "4h").
	// Queries without %s are used as-is.
	Query string
}

var prometheusQueries = []metricQuery{
	{"BytesInPerSec", "sum(rate(kafka_server_brokertopicmetrics_bytesinpersec_total[%s]))"},
	{"BytesOutPerSec", "sum(rate(kafka_server_brokertopicmetrics_bytesoutpersec_total[%s]))"},
	{"MessagesInPerSec", "sum(rate(kafka_server_brokertopicmetrics_messagesinpersec_total[%s]))"},
	{"PartitionCount", "sum(kafka_server_replicamanager_partitioncount)"},
	{"GlobalPartitionCount", "sum(kafka_server_replicamanager_partitioncount)"},
	{"ClientConnectionCount", "sum(kafka_server_socketservermetrics_connection_count)"},
	{"TotalLocalStorageUsage", "sum(kafka_log_log_size) / (1024*1024*1024)"},
}

// PrometheusService collects Kafka metrics from a Prometheus server
type PrometheusService struct {
	client *client.PrometheusClient
}

// NewPrometheusService creates a new Prometheus metrics service
func NewPrometheusService(promClient *client.PrometheusClient) *PrometheusService {
	return &PrometheusService{client: promClient}
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

	for _, mq := range prometheusQueries {
		query := mq.Query
		if strings.Contains(query, "%s") {
			query = fmt.Sprintf(query, rateWindow)
		}
		results, err := s.client.QueryRange(query, start, end, step)
		if err != nil {
			slog.Warn("Prometheus query failed, skipping metric", "label", mq.Label, "error", err)
			continue
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

	aggregates := calculateAggregates(valuesByLabel)

	return &types.ProcessedClusterMetrics{
		Metadata: types.MetricMetadata{
			StartDate: start,
			EndDate:   end,
			Period:    int32(step.Seconds()),
		},
		Metrics:    allMetrics,
		Aggregates: aggregates,
	}, nil
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
