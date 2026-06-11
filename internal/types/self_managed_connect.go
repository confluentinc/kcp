package types

type SelfManagedConnector struct {
	Name        string         `json:"name"`
	Config      map[string]any `json:"config"`
	State       string         `json:"state,omitempty"`
	ConnectHost string         `json:"connect_host,omitempty"`
}

type SelfManagedConnectors struct {
	Connectors []SelfManagedConnector `json:"connectors"`
}

// mergeSelfManagedConnectors merges connectors, with new taking precedence for duplicates (by name)
func mergeSelfManagedConnectors(newConnectors, oldConnectors *SelfManagedConnectors) *SelfManagedConnectors {
	if oldConnectors == nil || len(oldConnectors.Connectors) == 0 {
		return newConnectors
	}
	if newConnectors == nil || len(newConnectors.Connectors) == 0 {
		return oldConnectors
	}

	connectorsByName := make(map[string]SelfManagedConnector)
	for _, c := range oldConnectors.Connectors {
		connectorsByName[c.Name] = c
	}
	for _, c := range newConnectors.Connectors {
		connectorsByName[c.Name] = c // new takes precedence
	}

	merged := make([]SelfManagedConnector, 0, len(connectorsByName))
	for _, c := range connectorsByName {
		merged = append(merged, c)
	}

	return &SelfManagedConnectors{Connectors: merged}
}
