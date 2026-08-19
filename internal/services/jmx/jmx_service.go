package jmx

import (
	"context"
	"errors"
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
	Counters               []CounterMBeanConfig
	Gauges                 []GaugeMBeanConfig
	Controller             []GaugeMBeanConfig
	Aggregates             []AggregateMBeanConfig
	PerConnectorAggregates []AggregateMBeanConfig
	UnitConversions        map[string]float64
	// OverriddenNames is the set of metric labels whose MBean came from a
	// user-configured override rather than the default. A read failure for an
	// overridden MBean is actionable (the override was set precisely to fix an
	// empty result), so it is logged louder than a routine missing default.
	// Nil when no overrides are configured.
	OverriddenNames map[string]bool
}

// BrokerMetricDefinitions returns the standard Kafka broker metric definitions.
// overrides maps a logical label (e.g. "BytesInPerSec") to the MBean object name
// this cluster's Jolokia agent actually exposes; an entry with an empty value is
// ignored. Pass nil for the defaults.
func BrokerMetricDefinitions(overrides map[string]string) MetricDefinitions {
	overridden := map[string]bool{}
	name := func(label, def string) string {
		if v, ok := overrides[label]; ok && v != "" {
			overridden[label] = true
			return v
		}
		return def
	}

	defs := MetricDefinitions{
		Counters: []CounterMBeanConfig{
			{"BytesInPerSec", name("BytesInPerSec", "kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec")},
			{"BytesOutPerSec", name("BytesOutPerSec", "kafka.server:type=BrokerTopicMetrics,name=BytesOutPerSec")},
			{"MessagesInPerSec", name("MessagesInPerSec", "kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec")},
		},
		Gauges: []GaugeMBeanConfig{
			{"PartitionCount", name("PartitionCount", "kafka.server:type=ReplicaManager,name=PartitionCount"), "Value"},
		},
		Controller: []GaugeMBeanConfig{
			{"GlobalPartitionCount", name("GlobalPartitionCount", "kafka.controller:type=KafkaController,name=GlobalPartitionCount"), "Value"},
		},
		Aggregates: []AggregateMBeanConfig{
			{"ClientConnectionCount", name("ClientConnectionCount", "kafka.server:type=socket-server-metrics,listener=*,networkProcessor=*"), "connection-count"},
			{"TotalLocalStorageUsage", name("TotalLocalStorageUsage", "kafka.log:type=Log,name=Size,*"), "Value"},
		},
		UnitConversions: map[string]float64{
			"TotalLocalStorageUsage": 1024 * 1024 * 1024,
		},
	}
	if len(overridden) > 0 {
		defs.OverriddenNames = overridden
	}
	return defs
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
		},
		PerConnectorAggregates: []AggregateMBeanConfig{
			{"source-record-write-rate", "kafka.connect:type=source-task-metrics,connector=*,task=*", "source-record-write-rate"},
			{"source-record-poll-rate", "kafka.connect:type=source-task-metrics,connector=*,task=*", "source-record-poll-rate"},
			{"sink-record-read-rate", "kafka.connect:type=sink-task-metrics,connector=*,task=*", "sink-record-read-rate"},
			{"sink-record-send-rate", "kafka.connect:type=sink-task-metrics,connector=*,task=*", "sink-record-send-rate"},
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

	// warnedControllerMissing dedupes the "controller MBean not available" caveat
	// (keyed by MBean) to once per scan. collectRawSample runs on every poll for the
	// whole scan duration and a missing controller MBean is a persistent config state,
	// so warning per-poll would flood the console (this caveat is Warn+). Safe without
	// a mutex: collectRawSample is called sequentially from CollectOverDuration.
	warnedControllerMissing map[string]bool

	// warnedMetricIssue dedupes per-metric read-failure logging (keyed by metric
	// name) to once per scan, for the same reason as warnedControllerMissing:
	// collectRawSample polls every interval for the whole scan duration, and a
	// read failure (missing MBean, timeout, auth error) is typically a persistent
	// condition, not a transient blip — logging it on every poll would flood the
	// console/log. Not present at all is expected/normal on some clusters (e.g. no
	// sink connectors) so is logged at Debug; other errors (timeout, auth,
	// connection) are logged at Warn.
	warnedMetricIssue map[string]bool
}

// NewJMXService creates a new JMX service with Jolokia clients for each endpoint.
// entityName describes the endpoint type for query info descriptions (e.g. "broker" or "worker").
func NewJMXService(endpoints []string, defs MetricDefinitions, entityName string, opts ...client.JolokiaOption) *JMXService {
	clients := make([]*client.JolokiaClient, len(endpoints))
	for i, endpoint := range endpoints {
		clients[i] = client.NewJolokiaClient(endpoint, opts...)
	}
	return &JMXService{
		clients:                 clients,
		metrics:                 defs,
		entityName:              entityName,
		warnedControllerMissing: make(map[string]bool),
		warnedMetricIssue:       make(map[string]bool),
	}
}

// logMetricReadErrorOnce logs a metric-read failure at most once per metric
// name for the lifetime of the JMXService (mirrors warnedControllerMissing —
// collectRawSample polls every interval, so per-poll logging would flood the
// console/log for a persistent condition). An MBean/instance that simply
// isn't present (e.g. no sink connectors on this cluster) is expected/normal
// and logged at Debug; any other error (timeout, auth, connection) is logged
// at Warn.
func (s *JMXService) logMetricReadErrorOnce(metricName, msg string, err error) {
	if s.warnedMetricIssue[metricName] {
		return
	}
	s.warnedMetricIssue[metricName] = true

	switch {
	case errors.Is(err, client.ErrJolokiaMBeanNotFound) && !s.metrics.OverriddenNames[metricName]:
		slog.Debug(msg, "mbean", metricName, "error", err)
	default:
		// A not-found for an overridden MBean is actionable — the override was
		// configured precisely to point at an MBean this agent exposes — so it
		// is surfaced at Warn rather than the routine Debug.
		slog.Warn(msg, "mbean", metricName, "error", err)
	}
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
				s.logMetricReadErrorOnce(mb.Name, "Failed to read MBean", err)
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
		if !found && !s.warnedControllerMissing[mb.MBean] {
			slog.Warn("Controller MBean not available from any broker — metric will be omitted. Ensure your JMX exporter scrapes kafka.controller MBeans.",
				"mbean", mb.MBean, "metric", mb.Name)
			s.warnedControllerMissing[mb.MBean] = true
		}
	}

	for _, amb := range s.metrics.Aggregates {
		var total float64
		for _, brokerClient := range s.clients {
			val, err := brokerClient.ReadMBeanAggregate(ctx, amb.MBean, amb.Attribute)
			if err != nil {
				s.logMetricReadErrorOnce(amb.Name, "Failed to read aggregate MBean", err)
				continue
			}
			total += val
		}
		sample.gauges[amb.Name] = total
	}

	for _, amb := range s.metrics.PerConnectorAggregates {
		perConnector := map[string]float64{}
		for _, brokerClient := range s.clients {
			byLabel, err := brokerClient.ReadMBeanAggregateByLabel(ctx, amb.MBean, amb.Attribute, "connector")
			if err != nil {
				s.logMetricReadErrorOnce(amb.Name, "Failed to read per-connector aggregate MBean", err)
				continue
			}
			for connector, v := range byLabel {
				perConnector[connector] += v
			}
		}
		for connector, v := range perConnector {
			sample.gauges[fmt.Sprintf("%s (%s)", amb.Name, connector)] = v
		}
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
	slog.Debug("JMX baseline sample collected", "elapsed", "0s")

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

			slog.Debug("JMX snapshot collected",
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
			MetricName:      mb.Name,
			SourceType:      types.MetricBackendJolokia,
			Statistic:       statistic,
			Period:          periodSec,
			QueryDuration:   durationStr,
			MBeanPath:       mb.MBean,
			JolokiaURL:      exampleURL,
			CurlCommand:     fmt.Sprintf("curl '%s/read/%s'", exampleURL, mb.MBean),
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
			MetricName:      mb.Name,
			SourceType:      types.MetricBackendJolokia,
			Statistic:       statistic,
			Period:          periodSec,
			QueryDuration:   durationStr,
			MBeanPath:       mb.MBean,
			JolokiaURL:      exampleURL,
			CurlCommand:     fmt.Sprintf("curl '%s/read/%s/%s'", exampleURL, mb.MBean, mb.Attribute),
			AggregationNote: note,
		})
	}

	for _, mb := range defs.PerConnectorAggregates {
		statistic := fmt.Sprintf("%s per connector (not summed cluster-wide)", mb.Attribute)
		note := fmt.Sprintf(
			"Wildcard MBean pattern %s; the %s attribute is grouped by the connector MBean property and reported per connector across all matching MBeans on all %d %s(s). Add -u user:pass to the curl command if authentication is configured.",
			mb.MBean, mb.Attribute, endpointCount, entityName)
		infos = append(infos, types.MetricQueryInfo{
			MetricName:      mb.Name,
			SourceType:      types.MetricBackendJolokia,
			Statistic:       statistic,
			Period:          periodSec,
			QueryDuration:   durationStr,
			MBeanPath:       mb.MBean,
			JolokiaURL:      exampleURL,
			CurlCommand:     fmt.Sprintf("curl '%s/read/%s/%s'", exampleURL, mb.MBean, mb.Attribute),
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
