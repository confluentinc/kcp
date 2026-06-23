package jmx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

// CounterMBeanConfig defines a rate MBean whose Count field is a monotonic counter.
type CounterMBeanConfig struct {
	Name  string
	MBean string
}

// GaugeMBeanConfig defines a MBean that returns a point-in-time gauge value.
type GaugeMBeanConfig struct {
	Name     string
	MBean    string
	ValueKey string
}

// AggregateMBeanConfig defines a MBean that requires wildcard pattern + aggregation.
type AggregateMBeanConfig struct {
	Name      string
	MBean     string
	Attribute string
}

// MetricDefinitions holds all metric definitions for a JMX collection target.
type MetricDefinitions struct {
	Counters        []CounterMBeanConfig
	Gauges          []GaugeMBeanConfig
	Controller      []GaugeMBeanConfig
	Aggregates      []AggregateMBeanConfig
	UnitConversions map[string]float64
}

// BrokerMetricDefinitions returns the standard Kafka broker metric definitions.
func BrokerMetricDefinitions() MetricDefinitions {
	return MetricDefinitions{
		Counters: []CounterMBeanConfig{
			{"BytesInPerSec", "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec"},
			{"BytesOutPerSec", "kafka.server:type=BrokerTopicMetrics,name=BytesOutPerSec"},
			{"MessagesInPerSec", "kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec"},
		},
		Gauges: []GaugeMBeanConfig{
			{"PartitionCount", "kafka.server:type=ReplicaManager,name=PartitionCount", "Value"},
		},
		Controller: []GaugeMBeanConfig{
			{"GlobalPartitionCount", "kafka.controller:type=KafkaController,name=GlobalPartitionCount", "Value"},
		},
		Aggregates: []AggregateMBeanConfig{
			{"ClientConnectionCount", "kafka.server:type=socket-server-metrics,listener=*,networkProcessor=*", "connection-count"},
			{"TotalLocalStorageUsage", "kafka.log:type=Log,name=Size,*", "Value"},
		},
		UnitConversions: map[string]float64{
			"TotalLocalStorageUsage": 1024 * 1024 * 1024,
		},
	}
}

// ConnectMetricDefinitions returns metric definitions for Kafka Connect workers.
func ConnectMetricDefinitions() MetricDefinitions {
	return MetricDefinitions{
		Gauges: []GaugeMBeanConfig{
			{"connector-count", "kafka.connect:type=connect-worker-metrics", "connector-count"},
			{"task-count", "kafka.connect:type=connect-worker-metrics", "task-count"},
		},
		Aggregates: []AggregateMBeanConfig{
			{"incoming-byte-rate", "kafka.connect:client-id=*,type=connect-metrics", "incoming-byte-rate"},
			{"outgoing-byte-rate", "kafka.connect:client-id=*,type=connect-metrics", "outgoing-byte-rate"},
			{"connection-count", "kafka.connect:client-id=*,type=connect-metrics", "connection-count"},
			{"request-rate", "kafka.connect:client-id=*,type=connect-metrics", "request-rate"},
			{"source-record-write-rate", "kafka.connect:type=source-task-metrics,connector=*,task=*", "source-record-write-rate"},
			{"source-record-poll-rate", "kafka.connect:type=source-task-metrics,connector=*,task=*", "source-record-poll-rate"},
		},
	}
}

// rawSample holds raw counter and gauge readings from a single poll.
type rawSample struct {
	timestamp time.Time
	counters  map[string]float64
	gauges    map[string]float64
}

// jmxSnapshot holds computed metrics from a single poll interval.
// Internal to the jmx package — not serialized to state.
type jmxSnapshot struct {
	start   time.Time
	end     time.Time
	metrics map[string]float64
}

// JMXService collects JMX metrics from Kafka brokers via Jolokia
type JMXService struct {
	clients    []*client.JolokiaClient
	metrics    MetricDefinitions
	entityName string // "broker" or "worker" — used in query info descriptions
}

// NewJMXService creates a new JMX service with Jolokia clients for each endpoint.
// entityName describes the endpoint type for query info descriptions (e.g. "broker" or "worker").
func NewJMXService(endpoints []string, defs MetricDefinitions, entityName string, opts ...client.JolokiaOption) *JMXService {
	clients := make([]*client.JolokiaClient, len(endpoints))
	for i, endpoint := range endpoints {
		clients[i] = client.NewJolokiaClient(endpoint, opts...)
	}
	return &JMXService{clients: clients, metrics: defs, entityName: entityName}
}

// collectRawSample reads raw counter and gauge values from all brokers.
func (s *JMXService) collectRawSample(ctx context.Context) (*rawSample, error) {
	sample := &rawSample{
		timestamp: time.Now(),
		counters:  make(map[string]float64),
		gauges:    make(map[string]float64),
	}

	for _, mb := range s.metrics.Counters {
		for _, brokerClient := range s.clients {
			value, err := brokerClient.ReadMBean(ctx, mb.MBean)
			if err != nil {
				slog.Warn("Failed to read MBean", "mbean", mb.Name, "error", err)
				continue
			}
			if v, ok := value["Count"]; ok {
				if f, ok := toFloat64(v); ok {
					sample.counters[mb.Name] += f
				}
			}
		}
	}

	for _, mb := range s.metrics.Gauges {
		for _, brokerClient := range s.clients {
			value, err := brokerClient.ReadMBean(ctx, mb.MBean)
			if err != nil {
				slog.Warn("Failed to read MBean", "mbean", mb.Name, "error", err)
				continue
			}
			if v, ok := value[mb.ValueKey]; ok {
				if f, ok := toFloat64(v); ok {
					sample.gauges[mb.Name] += f
				}
			}
		}
	}

	// Controller MBeans only exist on the active controller broker.
	// Try all brokers; use the first successful response.
	for _, mb := range s.metrics.Controller {
		found := false
		for _, brokerClient := range s.clients {
			value, err := brokerClient.ReadMBean(ctx, mb.MBean)
			if err != nil {
				continue // Expected to fail on non-controller brokers
			}
			if v, ok := value[mb.ValueKey]; ok {
				if f, ok := toFloat64(v); ok {
					sample.gauges[mb.Name] = f
					found = true
					break
				}
			}
		}
		if !found {
			slog.Info("Controller MBean not available from any broker — metric will be omitted. Ensure your JMX exporter scrapes kafka.controller MBeans.",
				"mbean", mb.MBean, "metric", mb.Name)
		}
	}

	for _, amb := range s.metrics.Aggregates {
		var total float64
		for _, brokerClient := range s.clients {
			val, err := brokerClient.ReadMBeanAggregate(ctx, amb.MBean, amb.Attribute)
			if err != nil {
				slog.Warn("Failed to read aggregate MBean", "mbean", amb.Name, "error", err)
				continue
			}
			total += val
		}
		sample.gauges[amb.Name] = total
	}

	return sample, nil
}

// computeSnapshot computes metrics from two consecutive raw samples.
func computeSnapshot(prev, curr *rawSample, unitConversions map[string]float64) *jmxSnapshot {
	elapsed := curr.timestamp.Sub(prev.timestamp).Seconds()
	snapshot := &jmxSnapshot{
		start:   prev.timestamp,
		end:     curr.timestamp,
		metrics: make(map[string]float64),
	}

	for name, currCount := range curr.counters {
		if prevCount, ok := prev.counters[name]; ok && elapsed > 0 {
			delta := currCount - prevCount
			if delta < 0 {
				// Counter reset (e.g. broker restart) — skip this sample
				continue
			}
			snapshot.metrics[name] = delta / elapsed
		}
	}

	for name, value := range curr.gauges {
		snapshot.metrics[name] = value
	}

	for metricName, divisor := range unitConversions {
		if raw, ok := snapshot.metrics[metricName]; ok {
			snapshot.metrics[metricName] = raw / divisor
		}
	}

	return snapshot
}

// CollectOverDuration collects JMX metrics over a specified duration at regular intervals
// and returns them in ProcessedClusterMetrics format for direct use by the UI.
func (s *JMXService) CollectOverDuration(ctx context.Context, duration, interval time.Duration) (*types.ProcessedClusterMetrics, error) {
	if duration <= interval {
		return nil, fmt.Errorf("scan duration (%s) must be greater than poll interval (%s)", duration, interval)
	}

	startTime := time.Now()
	var snapshots []jmxSnapshot

	prevSample, err := s.collectRawSample(ctx)
	if err != nil {
		return nil, err
	}
	slog.Info("JMX baseline sample collected", "elapsed", "0s")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	deadline := startTime.Add(duration)

	brokerURLs := make([]string, len(s.clients))
	for i, c := range s.clients {
		brokerURLs[i] = c.BaseURL()
	}

	for {
		select {
		case <-ctx.Done():
			result := toProcessedClusterMetrics(snapshots, startTime, duration, interval)
			result.QueryInfo = buildJMXQueryInfo(brokerURLs, duration, interval, s.metrics, s.entityName)
			return result, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				result := toProcessedClusterMetrics(snapshots, startTime, duration, interval)
				result.QueryInfo = buildJMXQueryInfo(brokerURLs, duration, interval, s.metrics, s.entityName)
				return result, nil
			}

			currSample, err := s.collectRawSample(ctx)
			if err != nil {
				slog.Warn("Failed to collect JMX sample", "error", err)
				continue
			}

			snapshot := computeSnapshot(prevSample, currSample, s.metrics.UnitConversions)
			snapshots = append(snapshots, *snapshot)
			prevSample = currSample

			slog.Info("JMX snapshot collected",
				"count", len(snapshots),
				"elapsed", time.Since(startTime).Round(time.Second))
		}
	}
}

// toProcessedClusterMetrics converts internal JMX snapshots into the
// ProcessedClusterMetrics format used by the UI, matching CloudWatch output.
func toProcessedClusterMetrics(snapshots []jmxSnapshot, scanStart time.Time, scanDuration, pollInterval time.Duration) *types.ProcessedClusterMetrics {
	var metrics []types.ProcessedMetric
	for _, snap := range snapshots {
		start := snap.start.Format(time.RFC3339)
		end := snap.end.Format(time.RFC3339)
		for label, value := range snap.metrics {
			v := value
			metrics = append(metrics, types.ProcessedMetric{
				Start: start,
				End:   end,
				Label: label,
				Value: &v,
			})
		}
	}

	return &types.ProcessedClusterMetrics{
		Metadata: types.MetricMetadata{
			StartDate: scanStart,
			EndDate:   scanStart.Add(scanDuration),
			Period:    int32(pollInterval.Seconds()),
		},
		Metrics:    metrics,
		Aggregates: calculateAggregates(snapshots),
	}
}

func calculateAggregates(snapshots []jmxSnapshot) map[string]types.MetricAggregate {
	valuesByLabel := make(map[string][]float64)
	for _, snap := range snapshots {
		for label, value := range snap.metrics {
			valuesByLabel[label] = append(valuesByLabel[label], value)
		}
	}

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

// buildJMXQueryInfo generates MetricQueryInfo entries for all JMX metrics,
// including the MBean path and a curl command to reproduce the query.
// entityName describes what the endpoints represent (e.g. "broker" or "worker").
func buildJMXQueryInfo(endpointURLs []string, duration, interval time.Duration, defs MetricDefinitions, entityName string) []types.MetricQueryInfo {
	if len(endpointURLs) == 0 {
		return nil
	}
	endpointCount := len(endpointURLs)
	exampleURL := endpointURLs[0]
	durationStr := types.FormatQueryDuration(duration)
	periodSec := int32(interval.Seconds())

	var infos []types.MetricQueryInfo

	for _, mb := range defs.Counters {
		infos = append(infos, types.MetricQueryInfo{
			MetricName:    mb.Name,
			SourceType:    types.MetricBackendJolokia,
			Statistic:     fmt.Sprintf("Rate (delta/sec, summed across %ss)", entityName),
			Period:        periodSec,
			QueryDuration: durationStr,
			MBeanPath:     mb.MBean,
			JolokiaURL:    exampleURL,
			CurlCommand:   fmt.Sprintf("curl '%s/read/%s'", exampleURL, mb.MBean),
			AggregationNote: fmt.Sprintf(
				"Rate computed from the monotonic Count attribute of %s. Values are summed across all %d %s(s), then the rate is derived from the delta between consecutive polls. Add -u user:pass to the curl command if authentication is configured.",
				mb.MBean, endpointCount, entityName),
		})
	}

	for _, mb := range defs.Gauges {
		statistic := fmt.Sprintf("Sum across %ss", entityName)
		note := fmt.Sprintf(
			"Gauge value read from the %s attribute of %s. Summed across all %d %s(s). Add -u user:pass to the curl command if authentication is configured.",
			mb.ValueKey, mb.MBean, endpointCount, entityName)
		if endpointCount == 1 {
			statistic = fmt.Sprintf("Point-in-time value (per %s)", entityName)
			note = fmt.Sprintf(
				"Gauge value read from the %s attribute of %s on each %s. Add -u user:pass to the curl command if authentication is configured.",
				mb.ValueKey, mb.MBean, entityName)
		}
		infos = append(infos, types.MetricQueryInfo{
			MetricName:    mb.Name,
			SourceType:    types.MetricBackendJolokia,
			Statistic:     statistic,
			Period:        periodSec,
			QueryDuration: durationStr,
			MBeanPath:     mb.MBean,
			JolokiaURL:    exampleURL,
			CurlCommand:   fmt.Sprintf("curl '%s/read/%s'", exampleURL, mb.MBean),
			AggregationNote: note,
		})
	}

	for _, mb := range defs.Controller {
		infos = append(infos, types.MetricQueryInfo{
			MetricName:    mb.Name,
			SourceType:    types.MetricBackendJolokia,
			Statistic:     fmt.Sprintf("Controller value (single %s)", entityName),
			Period:        periodSec,
			QueryDuration: durationStr,
			MBeanPath:     mb.MBean,
			JolokiaURL:    exampleURL,
			CurlCommand:   fmt.Sprintf("curl '%s/read/%s'", exampleURL, mb.MBean),
			AggregationNote: fmt.Sprintf(
				"Controller-only MBean %s; queried from the active controller %s. This MBean must be exposed by the Jolokia agent. Add -u user:pass to the curl command if authentication is configured.",
				mb.MBean, entityName),
		})
	}

	for _, mb := range defs.Aggregates {
		statistic := fmt.Sprintf("Sum of %s across matching instances", mb.Attribute)
		note := fmt.Sprintf(
			"Wildcard MBean pattern %s; the %s attribute is summed across all matching MBeans on all %d %s(s). Add -u user:pass to the curl command if authentication is configured.",
			mb.MBean, mb.Attribute, endpointCount, entityName)
		infos = append(infos, types.MetricQueryInfo{
			MetricName:    mb.Name,
			SourceType:    types.MetricBackendJolokia,
			Statistic:     statistic,
			Period:        periodSec,
			QueryDuration: durationStr,
			MBeanPath:     mb.MBean,
			JolokiaURL:    exampleURL,
			CurlCommand:   fmt.Sprintf("curl '%s/read/%s/%s'", exampleURL, mb.MBean, mb.Attribute),
			AggregationNote: note,
		})
	}

	return infos
}

func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}
