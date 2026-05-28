package types

type ConnectAuthMethod string

const (
	ConnectAuthMethodSaslScram       ConnectAuthMethod = "SASL/SCRAM"
	ConnectAuthMethodTls             ConnectAuthMethod = "TLS"
	ConnectAuthMethodUnauthenticated ConnectAuthMethod = "Unauthenticated"
)

type ConnectSaslScramAuth struct {
	Username string
	Password string
}

type ConnectTlsAuth struct {
	CACert     string
	ClientCert string
	ClientKey  string
}

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

	result := &SelfManagedConnectors{Connectors: merged}

	// Preserve metrics: prefer new, fall back to old
	if newConnectors.Metrics != nil {
		result.Metrics = newConnectors.Metrics
	} else if oldConnectors.Metrics != nil {
		result.Metrics = oldConnectors.Metrics
	}

	return result
}

func (c *KafkaAdminClientInformation) SetSelfManagedConnectors(connectors []SelfManagedConnector) {
	// Preserve existing metrics when updating connectors
	var existingMetrics *ProcessedClusterMetrics
	if c.SelfManagedConnectors != nil {
		existingMetrics = c.SelfManagedConnectors.Metrics
	}
	c.SelfManagedConnectors = &SelfManagedConnectors{
		Connectors: connectors,
		Metrics:    existingMetrics,
	}
}
