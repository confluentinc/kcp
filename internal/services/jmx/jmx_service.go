package jmx

import (
	"context"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

// counterMBeanConfig defines a rate MBean whose Count field is a monotonic counter.
// Actual rates are computed from deltas between consecutive counter readings.
type counterMBeanConfig struct {
	Name string // Metric name matching CloudWatch (e.g., "BytesInPerSec")
	MBean string // MBean path
}

// gaugeMBeanConfig defines a MBean that returns a point-in-time gauge value.
type gaugeMBeanConfig struct {
	Name     string // Metric name matching CloudWatch
	MBean    string // MBean path
	ValueKey string // Attribute key to read (e.g., "Value")
}

// aggregateMBeanConfig defines a MBean that requires wildcard pattern + aggregation.
type aggregateMBeanConfig struct {
	Name      string // Metric name matching CloudWatch
	MBean     string // MBean wildcard pattern
	Attribute string // Attribute to read and sum
}

// counterMBeans are rate metrics where we read the Count (monotonic counter)
// and compute actual rates from deltas between consecutive readings.
var counterMBeans = []counterMBeanConfig{
	{"BytesInPerSec", "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec"},
	{"BytesOutPerSec", "kafka.server:type=BrokerTopicMetrics,name=BytesOutPerSec"},
	{"MessagesInPerSec", "kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec"},
}

// gaugeMBeans are point-in-time values read directly.
var gaugeMBeans = []gaugeMBeanConfig{
	{"PartitionCount", "kafka.server:type=ReplicaManager,name=PartitionCount", "Value"},
}

// aggregateMBeans are per-partition or per-listener MBeans
// that need wildcard queries with summing across all matches.
var aggregateMBeans = []aggregateMBeanConfig{
	{"ClientConnectionCount", "kafka.server:type=socket-server-metrics,listener=*,networkProcessor=*", "connection-count"},
	{"TotalLocalStorageUsage", "kafka.log:type=Log,name=Size,*", "Value"},
}

// rawSample holds raw counter and gauge readings from a single poll.
// This is an internal type — not stored in state.
type rawSample struct {
	timestamp time.Time
	counters  map[string]float64 // monotonic counter values (e.g., BytesInPerSec Count)
	gauges    map[string]float64 // point-in-time gauge values (e.g., PartitionCount)
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

	// Read counter MBeans (monotonic Count field)
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

	// Read gauge MBeans (point-in-time values)
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

	// Read aggregate MBeans (wildcard patterns summed across matches)
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

// computeSnapshot computes a metrics snapshot from two consecutive raw samples.
// Counter deltas are divided by elapsed time to produce actual rates.
// Gauge values are taken from the current (second) sample.
func computeSnapshot(prev, curr *rawSample) *types.JMXMetricSnapshot {
	elapsed := curr.timestamp.Sub(prev.timestamp).Seconds()
	snapshot := &types.JMXMetricSnapshot{
		Timestamp: curr.timestamp,
		Metrics:   make(map[string]float64),
	}

	// Compute actual rates from counter deltas
	for name, currCount := range curr.counters {
		if prevCount, ok := prev.counters[name]; ok && elapsed > 0 {
			snapshot.Metrics[name] = (currCount - prevCount) / elapsed
		}
	}

	// Copy gauge values directly
	for name, value := range curr.gauges {
		snapshot.Metrics[name] = value
	}

	// Derive GlobalPartitionCount
	if pc, ok := snapshot.Metrics["PartitionCount"]; ok {
		snapshot.Metrics["GlobalPartitionCount"] = pc
	}

	// Convert TotalLocalStorageUsage from bytes to GB
	if bytes, ok := snapshot.Metrics["TotalLocalStorageUsage"]; ok {
		snapshot.Metrics["TotalLocalStorageUsage"] = bytes / (1024 * 1024 * 1024)
	}

	return snapshot
}

// CollectSnapshot collects a single snapshot by taking two raw samples
// separated by a short interval and computing rates from counter deltas.
func (s *JMXService) CollectSnapshot(ctx context.Context) (*types.JMXMetricSnapshot, error) {
	first, err := s.collectRawSample(ctx)
	if err != nil {
		return nil, err
	}

	time.Sleep(1 * time.Second)

	second, err := s.collectRawSample(ctx)
	if err != nil {
		return nil, err
	}

	return computeSnapshot(first, second), nil
}

// CollectOverDuration collects JMX metrics over a specified duration at regular intervals.
// Takes raw counter readings at each interval and computes actual rates from
// consecutive counter deltas — not EWMA averages.
func (s *JMXService) CollectOverDuration(ctx context.Context, duration, interval time.Duration) (*types.JMXMetrics, error) {
	startTime := time.Now()
	metrics := &types.JMXMetrics{
		ScanDuration:  duration.String(),
		ScanStartTime: startTime,
		Snapshots:     make([]types.JMXMetricSnapshot, 0),
	}

	// Take initial raw sample (baseline for first rate computation)
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
			return metrics, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return metrics, nil
			}

			currSample, err := s.collectRawSample(ctx)
			if err != nil {
				slog.Warn("Failed to collect JMX sample", "error", err)
				continue
			}

			snapshot := computeSnapshot(prevSample, currSample)
			metrics.Snapshots = append(metrics.Snapshots, *snapshot)
			prevSample = currSample

			slog.Info("JMX snapshot collected",
				"count", len(metrics.Snapshots),
				"elapsed", time.Since(startTime).Round(time.Second))
		}
	}
}

// toFloat64 converts an interface{} value to float64.
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
