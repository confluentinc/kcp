package types

type SelfManagedConnector struct {
	Name        string         `json:"name"`
	Config      map[string]any `json:"config"`
	State       string         `json:"state,omitempty"`
	ConnectHost string         `json:"connect_host,omitempty"`
}

type SelfManagedConnectors struct {
	Connectors []SelfManagedConnector   `json:"connectors"`
	Metrics    *ProcessedClusterMetrics `json:"metrics,omitempty"`
}
