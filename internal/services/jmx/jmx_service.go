package jmx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

// counterMBeanConfig defines a rate MBean whose Count field is a monotonic counter.
type counterMBeanConfig struct {
	Name  string
	MBean string
}

// gaugeMBeanConfig defines a MBean that returns a point-in-time gauge value.
type gaugeMBeanConfig struct {
	Name     string
	MBean    string
	ValueKey string
}

// aggregateMBeanConfig defines a MBean that requires wildcard pattern + aggregation.
type aggregateMBeanConfig struct {
	Name      string
	MBean     string
	Attribute string
}

var counterMBeans = []counterMBeanConfig{
	{"BytesInPerSec", "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec"},
	{"BytesOutPerSec", "kafka.server:type=BrokerTopicMetrics,name=BytesOutPerSec"},
	{"MessagesInPerSec", "kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec"},
}

var gaugeMBeans = []gaugeMBeanConfig{
	{"PartitionCount", "kafka.server:type=ReplicaManager,name=PartitionCount", "Value"},
}

var aggregateMBeans = []aggregateMBeanConfig{
	{"ClientConnectionCount", "kafka.server:type=socket-server-metrics,listener=*,networkProcessor=*", "connection-count"},
	{"TotalLocalStorageUsage", "kafka.log:type=Log,name=Size,*", "Value"},
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
	clients []*client.JolokiaClient
}

// NewJMXService creates a new JMX service with Jolokia clients for each broker endpoint
func NewJMXService(endpoints []string, opts ...client.JolokiaOption) *JMXService {
	clients := make([]*client.JolokiaClient, len(endpoints))
	for i, endpoint := range endpoints {
		clients[i] = client.NewJolokiaClient(endpoint, opts...)
	}
	return &JMXService{clients: clients}
}

// collectRawSample reads raw counter and gauge values from all brokers.
func (s *JMXService) collectRawSample(ctx context.Context) (*rawSample, error) {
	sample := &rawSample{
		timestamp: time.Now(),
		counters:  make(map[string]float64),
		gauges:    make(map[string]float64),
	}

	for _, mb := range counterMBeans {
		for _, brokerClient := range s.clients {
			value, err := brokerClient.ReadMBean(mb.MBean)
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

	for _, mb := range gaugeMBeans {
		for _, brokerClient := range s.clients {
			value, err := brokerClient.ReadMBean(mb.MBean)
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

	for _, amb := range aggregateMBeans {
		var total float64
		for _, brokerClient := range s.clients {
			val, err := brokerClient.ReadMBeanAggregate(amb.MBean, amb.Attribute)
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
func computeSnapshot(prev, curr *rawSample) *jmxSnapshot {
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

	if pc, ok := snapshot.metrics["PartitionCount"]; ok {
		snapshot.metrics["GlobalPartitionCount"] = pc
	}

	if bytes, ok := snapshot.metrics["TotalLocalStorageUsage"]; ok {
		snapshot.metrics["TotalLocalStorageUsage"] = bytes / (1024 * 1024 * 1024)
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

	for {
		select {
		case <-ctx.Done():
			return toProcessedClusterMetrics(snapshots, startTime, duration, interval), ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return toProcessedClusterMetrics(snapshots, startTime, duration, interval), nil
			}

			currSample, err := s.collectRawSample(ctx)
			if err != nil {
				slog.Warn("Failed to collect JMX sample", "error", err)
				continue
			}

			snapshot := computeSnapshot(prevSample, currSample)
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
