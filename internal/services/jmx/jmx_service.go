package jmx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/types"
)

// mbeanConfig defines a single MBean to query
type mbeanConfig struct {
	Name     string // Metric name matching CloudWatch (e.g., "BytesInPerSec")
	MBean    string // MBean path (e.g., "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec")
	IsRate   bool   // If true, extract rate fields; if false, use ValueKey
	ValueKey string // Key to read from value map when IsRate=false (e.g., "Value")
}

// mbeans defines all Kafka JMX MBeans to query.
// Names are aligned with CloudWatch metric names for UI parity.
var mbeans = []mbeanConfig{
	{
		Name:   "BytesInPerSec",
		MBean:  "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec",
		IsRate: true,
	},
	{
		Name:   "BytesOutPerSec",
		MBean:  "kafka.server:type=BrokerTopicMetrics,name=BytesOutPerSec",
		IsRate: true,
	},
	{
		Name:   "MessagesInPerSec",
		MBean:  "kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec",
		IsRate: true,
	},
	{
		Name:     "PartitionCount",
		MBean:    "kafka.server:type=ReplicaManager,name=PartitionCount",
		IsRate:   false,
		ValueKey: "Value",
	},
	{
		Name:     "ClientConnectionCount",
		MBean:    "kafka.server:type=socket-server-metrics,name=connection-count",
		IsRate:   false,
		ValueKey: "Value",
	},
	{
		Name:     "TotalLocalStorageUsage",
		MBean:    "kafka.log:type=Log,name=Size",
		IsRate:   false,
		ValueKey: "Value",
	},
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

// CollectSnapshot collects a single snapshot of all JMX metrics.
// Metric naming is aligned with CloudWatch for UI parity:
//   - Rate metrics: primary value uses CloudWatch name (e.g. "BytesInPerSec") from OneMinuteRate,
//     supplementary rates stored with suffixes (e.g. "BytesInPerSec_FiveMinuteRate")
//   - Non-rate metrics: stored directly (e.g. "PartitionCount", "ClientConnectionCount")
//   - GlobalPartitionCount: derived as sum of per-broker PartitionCount
func (s *JMXService) CollectSnapshot(ctx context.Context) (*types.JMXMetricSnapshot, error) {
	snapshot := &types.JMXMetricSnapshot{
		Timestamp: time.Now(),
		Metrics:   make(map[string]float64),
	}

	// Query each MBean across all brokers
	for _, mb := range mbeans {
		metricValues := make(map[string]float64)

		for _, brokerClient := range s.clients {
			value, err := brokerClient.ReadMBean(mb.MBean)
			if err != nil {
				slog.Warn("Failed to read MBean",
					"mbean", mb.Name,
					"path", mb.MBean,
					"error", err)
				continue
			}

			if mb.IsRate {
				// Primary value: OneMinuteRate stored as the CloudWatch-aligned metric name
				if v, ok := value["OneMinuteRate"]; ok {
					if f, ok := toFloat64(v); ok {
						metricValues[mb.Name] += f
					}
				}
				// Supplementary rates stored with suffixes
				for _, field := range []string{"FiveMinuteRate", "FifteenMinuteRate", "Count", "MeanRate"} {
					if v, ok := value[field]; ok {
						if f, ok := toFloat64(v); ok {
							metricValues[fmt.Sprintf("%s_%s", mb.Name, field)] += f
						}
					}
				}
			} else {
				if v, ok := value[mb.ValueKey]; ok {
					if f, ok := toFloat64(v); ok {
						metricValues[mb.Name] += f
					}
				}
			}
		}

		for key, value := range metricValues {
			snapshot.Metrics[key] = value
		}
	}

	// Derive GlobalPartitionCount (same as PartitionCount but matches CloudWatch naming)
	if pc, ok := snapshot.Metrics["PartitionCount"]; ok {
		snapshot.Metrics["GlobalPartitionCount"] = pc
	}

	// Convert TotalLocalStorageUsage from bytes to GB for CloudWatch parity
	if bytes, ok := snapshot.Metrics["TotalLocalStorageUsage"]; ok {
		snapshot.Metrics["TotalLocalStorageUsage"] = bytes / (1024 * 1024 * 1024)
	}

	return snapshot, nil
}

// CollectOverDuration collects JMX metrics over a specified duration at regular intervals
func (s *JMXService) CollectOverDuration(ctx context.Context, duration, interval time.Duration) (*types.JMXMetrics, error) {
	startTime := time.Now()
	metrics := &types.JMXMetrics{
		ScanDuration:  duration.String(),
		ScanStartTime: startTime,
		Snapshots:     make([]types.JMXMetricSnapshot, 0),
	}

	// Collect first snapshot immediately
	snapshot, err := s.CollectSnapshot(ctx)
	if err != nil {
		slog.Warn("Failed to collect initial JMX snapshot", "error", err)
	} else {
		metrics.Snapshots = append(metrics.Snapshots, *snapshot)
		slog.Info("JMX snapshot collected",
			"count", len(metrics.Snapshots),
			"elapsed", time.Since(startTime).Round(time.Second))
	}

	// Set up ticker for subsequent snapshots
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	deadline := startTime.Add(duration)

	for {
		select {
		case <-ctx.Done():
			return metrics, ctx.Err()
		case <-ticker.C:
			// Check if we've exceeded the duration
			if time.Now().After(deadline) {
				return metrics, nil
			}

			// Collect snapshot
			snapshot, err := s.CollectSnapshot(ctx)
			if err != nil {
				slog.Warn("Failed to collect JMX snapshot", "error", err)
				continue
			}

			metrics.Snapshots = append(metrics.Snapshots, *snapshot)
			slog.Info("JMX snapshot collected",
				"count", len(metrics.Snapshots),
				"elapsed", time.Since(startTime).Round(time.Second))
		}
	}
}

// toFloat64 converts an interface{} value to float64
// Handles float64, int, and int64 types from JSON unmarshaling
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
