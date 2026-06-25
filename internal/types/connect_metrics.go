package types

import "time"

// ConnectMetricMetadata describes a self-managed Kafka Connect metrics collection.
// Unlike the broker-oriented MetricMetadata, it carries only fields meaningful in
// the Connect scope: the collection window, the polling granularity, and which
// backend produced the data. MetricsSource reuses the shared MetricBackend type
// (jolokia|prometheus for Connect) so it stays consistent with QueryInfo.SourceType
// and the collector dispatch; the JSON wire value is unchanged. metrics_source is
// intentionally NOT omitempty — it belongs to every Connect metrics block; legacy
// state files (collected before this field existed) load with it empty.
type ConnectMetricMetadata struct {
	StartDate     time.Time     `json:"start_date"`
	EndDate       time.Time     `json:"end_date"`
	Period        int32         `json:"period"`
	MetricsSource MetricBackend `json:"metrics_source"`
}

// ConnectClusterMetrics is the purpose-built metrics envelope for self-managed
// Kafka Connect. It mirrors the consumable parts of ProcessedClusterMetrics
// (results, aggregates, query_info) but drops the broker-only metadata and the
// region/cluster_arn that are meaningless for a Connect cluster.
type ConnectClusterMetrics struct {
	Metadata   ConnectMetricMetadata      `json:"metadata"`
	Metrics    []ProcessedMetric          `json:"results"`
	Aggregates map[string]MetricAggregate `json:"aggregates"`
	QueryInfo  []MetricQueryInfo          `json:"query_info"`
}
