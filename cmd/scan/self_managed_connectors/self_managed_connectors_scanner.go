package self_managed_connectors

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/confluentinc/kcp/internal/redact"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

type ConnectAPIClient interface {
	ListConnectors() ([]string, error)
	GetConnectorConfig(name string) (map[string]any, error)
	GetConnectorStatus(name string) (map[string]any, error)
}

type HTTPConnectClient struct {
	baseURL    string
	httpClient *http.Client
	authMethod types.ConnectAuthMethod
	saslAuth   types.ConnectSaslScramAuth
}

type SelfManagedConnectorsScannerOpts struct {
	StateFile      string
	State          *types.State
	ConnectRestURL string
	ClusterArn     string
	AuthMethod     types.ConnectAuthMethod
	SaslScramAuth  types.ConnectSaslScramAuth
	TlsAuth        types.ConnectTlsAuth
}

type SelfManagedConnectorsScanner struct {
	StateFile  string
	State      *types.State
	ClusterArn string
	client     ConnectAPIClient
}

func NewSelfManagedConnectorsScanner(opts SelfManagedConnectorsScannerOpts) (*SelfManagedConnectorsScanner, error) {
	httpClient, err := createHTTPClient(opts.AuthMethod, opts.TlsAuth)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	connectClient := &HTTPConnectClient{
		baseURL:    opts.ConnectRestURL,
		httpClient: httpClient,
		authMethod: opts.AuthMethod,
		saslAuth:   opts.SaslScramAuth,
	}

	return &SelfManagedConnectorsScanner{
		StateFile:  opts.StateFile,
		State:      opts.State,
		ClusterArn: opts.ClusterArn,
		client:     connectClient,
	}, nil
}

func createHTTPClient(authMethod types.ConnectAuthMethod, tlsAuth types.ConnectTlsAuth) (*http.Client, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	// Only configure TLS if using TLS client certificate authentication
	if authMethod == types.ConnectAuthMethodTls {
		caCert, err := os.ReadFile(tlsAuth.CACert)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		clientCert, err := tls.LoadX509KeyPair(tlsAuth.ClientCert, tlsAuth.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate and key: %w", err)
		}

		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      caCertPool,
				Certificates: []tls.Certificate{clientCert},
			},
		}
	}

	return client, nil
}

func (s *SelfManagedConnectorsScanner) Run() error {
	if s.client == nil {
		return fmt.Errorf("connect API client not initialized")
	}

	fmt.Printf("🚀 Starting self-managed connector scan for cluster %s\n", utils.ExtractClusterNameFromArn(s.ClusterArn))

	connectorNames, err := s.client.ListConnectors()
	if err != nil {
		return fmt.Errorf("failed to list connectors: %v", err)
	}

	fmt.Printf("  🔍 Found %d connectors\n", len(connectorNames))

	if len(connectorNames) == 0 {
		fmt.Printf("  ⏭️  No connectors found for cluster %s, skipping\n", utils.ExtractClusterNameFromArn(s.ClusterArn))
		return nil
	}

	connectors := []types.SelfManagedConnector{}
	totalRedacted := 0
	for _, name := range connectorNames {
		connector, redactedCount, err := s.getConnectorDetails(name)
		if err != nil {
			slog.Warn(fmt.Sprintf("⚠️ failed to get connector details for connector %s: %v", name, err))
			continue
		}
		totalRedacted += redactedCount
		connectors = append(connectors, connector)
	}

	fmt.Printf("  ✅ Successfully retrieved connector details for %d connectors\n", len(connectors))
	if totalRedacted > 0 {
		// Counts only — never the redacted keys or values.
		slog.Info("redacted sensitive connector config fields", "redacted_fields", totalRedacted, "connectors", len(connectors))
	}

	if err := s.updateStateWithConnectors(connectors); err != nil {
		return fmt.Errorf("failed to update state: %v", err)
	}

	if err := s.State.PersistStateFile(s.StateFile); err != nil {
		return fmt.Errorf("failed to save state file: %v", err)
	}

	fmt.Printf("✅ Self-managed connector scan complete for cluster %s\n", utils.ExtractClusterNameFromArn(s.ClusterArn))
	return nil
}

// getConnectorDetails fetches a connector's config and status. The config is
// redacted (sensitive values replaced) before it is stored on the connector, so
// raw secrets never enter the persisted state. Returns the connector, the number
// of redacted fields, and any error.
func (s *SelfManagedConnectorsScanner) getConnectorDetails(name string) (types.SelfManagedConnector, int, error) {
	connector := types.SelfManagedConnector{
		Name: name,
	}

	config, err := s.client.GetConnectorConfig(name)
	if err != nil {
		return connector, 0, fmt.Errorf("failed to get config: %w", err)
	}
	redactedConfig, redactedCount := redact.RedactAnyMap(config)
	connector.Config = redactedConfig

	status, err := s.client.GetConnectorStatus(name)
	if err != nil {
		slog.Warn(fmt.Sprintf("⚠️ failed to get connector status for connector %s: %v", name, err))
	} else {
		if state, ok := status["connector"].(map[string]any); ok {
			if stateStr, ok := state["state"].(string); ok {
				connector.State = stateStr
			}
		}
	}

	return connector, redactedCount, nil
}

func (c *HTTPConnectClient) ListConnectors() ([]string, error) {
	url := fmt.Sprintf("%s/connectors", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.addAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var connectorNames []string
	if err := json.NewDecoder(resp.Body).Decode(&connectorNames); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return connectorNames, nil
}

func (c *HTTPConnectClient) GetConnectorConfig(name string) (map[string]any, error) {
	url := fmt.Sprintf("%s/connectors/%s/config", c.baseURL, name)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.addAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var config map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return config, nil
}

func (c *HTTPConnectClient) GetConnectorStatus(name string) (map[string]any, error) {
	url := fmt.Sprintf("%s/connectors/%s/status", c.baseURL, name)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.addAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return status, nil
}

// addAuthHeaders adds basic authentication for SASL/SCRAM Connect clusters on the
// list/status/config endpoints.
func (c *HTTPConnectClient) addAuthHeaders(req *http.Request) {
	if c.authMethod == types.ConnectAuthMethodSaslScram {
		req.SetBasicAuth(c.saslAuth.Username, c.saslAuth.Password)
	}
}

func (s *SelfManagedConnectorsScanner) updateStateWithConnectors(connectors []types.SelfManagedConnector) error {
	cluster, err := s.State.GetClusterByArn(s.ClusterArn)
	if err != nil {
		return err
	}

	cluster.KafkaAdminClientInformation.SetSelfManagedConnectors(connectors)
	fmt.Printf("✅ Updated cluster %s with self-managed connector information\n", utils.ExtractClusterNameFromArn(s.ClusterArn))

	return nil
}
