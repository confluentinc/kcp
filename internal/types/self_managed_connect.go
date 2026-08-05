package types

// Connector is a single self-managed Kafka Connect connector discovered from a
// Connect REST endpoint. Metrics holds this connector's own source/sink-task
// rates once per-connector collection runs (Plan 2); it is nil until then.
type Connector struct {
	Name   string         `json:"name"`
	State  string         `json:"state,omitempty"`
	Config map[string]any `json:"config"`
	// ConnectHost is the worker (worker_id from the Connect /status API) currently
	// running this connector. Informational only — NOT a grouping/merge key, since
	// it can change on rebalance and differs per worker in a distributed cluster.
	ConnectHost string                 `json:"connect_host,omitempty"`
	Metrics     *ConnectClusterMetrics `json:"metrics,omitempty"`
}

// ConnectCluster groups the connectors discovered from one Connect REST endpoint
// (the scan's --connect-rest-url), the stable identity of "which Connect cluster".
// Metrics holds the cluster-level (worker/client) metrics for this endpoint.
type ConnectCluster struct {
	ConnectRestURL string                 `json:"connect_rest_url"`
	Metrics        *ConnectClusterMetrics `json:"metrics,omitempty"`
	Connectors     []Connector            `json:"connectors"`
}
