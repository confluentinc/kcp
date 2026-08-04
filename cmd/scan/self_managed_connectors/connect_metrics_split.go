package self_managed_connectors

import (
	"strings"

	"github.com/confluentinc/kcp/internal/types"
)

// parseConnectorLabel splits a transient collector label "<metric> (<connector>)"
// into its bare metric name and connector. ok is false for a cluster-level label
// (no " (...)" suffix), in which case the label is returned unchanged as metric.
func parseConnectorLabel(label string) (metric, connector string, ok bool) {
	if !strings.HasSuffix(label, ")") {
		return label, "", false
	}
	idx := strings.LastIndex(label, " (")
	if idx < 0 {
		return label, "", false
	}
	return label[:idx], label[idx+2 : len(label)-1], true
}

// splitConnectMetrics partitions one collected ConnectClusterMetrics into the
// cluster-level metrics (bare cluster labels only) and a per-connector map
// keyed by connector name, each with bare metric labels and a copy of the
// shared Metadata.
//
// Results and Aggregates carry the transient composite label
// "<metric> (<connector>)" emitted by the JMX/Prometheus collectors for
// per-connector metrics; parseConnectorLabel strips the suffix and routes the
// entry to that connector's block (recording the bare metric name), or, for a
// bare cluster label, to the cluster block.
//
// QueryInfo is different: collectors emit ONE query_info per metric with a
// BARE MetricName (it describes the wildcard collection method, not a single
// connector's series), never the composite label. So a query_info is routed
// by looking up whether its bare MetricName was seen as a per-connector
// metric: if so, it is copied into every connector block that actually has
// that metric (in its Results or Aggregates); otherwise it is a cluster/client
// metric's query_info and stays on the cluster block.
func splitConnectMetrics(cm *types.ConnectClusterMetrics) (*types.ConnectClusterMetrics, map[string]*types.ConnectClusterMetrics) {
	if cm == nil {
		return nil, nil
	}

	cluster := &types.ConnectClusterMetrics{
		Metadata:   cm.Metadata,
		Aggregates: map[string]types.MetricAggregate{},
	}
	per := map[string]*types.ConnectClusterMetrics{}
	perConnectorMetricNames := map[string]bool{}

	ensure := func(connector string) *types.ConnectClusterMetrics {
		if per[connector] == nil {
			per[connector] = &types.ConnectClusterMetrics{
				Metadata:   cm.Metadata,
				Aggregates: map[string]types.MetricAggregate{},
			}
		}
		return per[connector]
	}

	for _, m := range cm.Metrics {
		metric, connector, ok := parseConnectorLabel(m.Label)
		if !ok {
			cluster.Metrics = append(cluster.Metrics, m)
			continue
		}
		m.Label = metric
		c := ensure(connector)
		c.Metrics = append(c.Metrics, m)
		perConnectorMetricNames[metric] = true
	}

	for label, agg := range cm.Aggregates {
		metric, connector, ok := parseConnectorLabel(label)
		if !ok {
			cluster.Aggregates[label] = agg
			continue
		}
		ensure(connector).Aggregates[metric] = agg
		perConnectorMetricNames[metric] = true
	}

	for _, qi := range cm.QueryInfo {
		if !perConnectorMetricNames[qi.MetricName] {
			cluster.QueryInfo = append(cluster.QueryInfo, qi)
			continue
		}
		for _, c := range per {
			if connectorHasMetric(c, qi.MetricName) {
				c.QueryInfo = append(c.QueryInfo, qi)
			}
		}
	}

	return cluster, per
}

// connectorHasMetric reports whether a connector's split block has the given
// bare metric name in its Results or Aggregates.
func connectorHasMetric(c *types.ConnectClusterMetrics, metric string) bool {
	for _, m := range c.Metrics {
		if m.Label == metric {
			return true
		}
	}
	_, ok := c.Aggregates[metric]
	return ok
}
